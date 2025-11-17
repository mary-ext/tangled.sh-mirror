// heavily inspired by gitea's model (basically copy-pasted)
package pulls_indexer

import (
	"context"
	"errors"
	"log"
	"os"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/custom"
	"github.com/blevesearch/bleve/v2/analysis/token/camelcase"
	"github.com/blevesearch/bleve/v2/analysis/token/lowercase"
	"github.com/blevesearch/bleve/v2/analysis/token/unicodenorm"
	"github.com/blevesearch/bleve/v2/analysis/tokenizer/unicode"
	"github.com/blevesearch/bleve/v2/index/upsidedown"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/indexer/base36"
	"tangled.org/core/appview/indexer/bleve"
	"tangled.org/core/appview/models"
	tlog "tangled.org/core/log"
)

const (
	pullIndexerAnalyzer = "pullIndexer"
	pullIndexerDocType  = "pullIndexerDocType"

	unicodeNormalizeName = "uicodeNormalize"
)

type Indexer struct {
	indexer bleve.Index
	path    string
}

func NewIndexer(indexDir string) *Indexer {
	return &Indexer{
		path: indexDir,
	}
}

// Init initializes the indexer
func (ix *Indexer) Init(ctx context.Context, e db.Execer) {
	l := tlog.FromContext(ctx)
	existed, err := ix.intialize(ctx)
	if err != nil {
		log.Fatalln("failed to initialize pull indexer", err)
	}
	if !existed {
		l.Debug("Populating the pull indexer")
		err := PopulateIndexer(ctx, ix, e)
		if err != nil {
			log.Fatalln("failed to populate pull indexer", err)
		}
	}

	count, _ := ix.indexer.DocCount()
	l.Info("Initialized the pull indexer", "docCount", count)
}

func generatePullIndexMapping() (mapping.IndexMapping, error) {
	mapping := bleve.NewIndexMapping()
	docMapping := bleve.NewDocumentMapping()

	textFieldMapping := bleve.NewTextFieldMapping()
	textFieldMapping.Store = false
	textFieldMapping.IncludeInAll = false

	keywordFieldMapping := bleve.NewKeywordFieldMapping()
	keywordFieldMapping.Store = false
	keywordFieldMapping.IncludeInAll = false

	// numericFieldMapping := bleve.NewNumericFieldMapping()

	docMapping.AddFieldMappingsAt("title", textFieldMapping)
	docMapping.AddFieldMappingsAt("body", textFieldMapping)

	docMapping.AddFieldMappingsAt("repo_at", keywordFieldMapping)
	docMapping.AddFieldMappingsAt("state", keywordFieldMapping)

	err := mapping.AddCustomTokenFilter(unicodeNormalizeName, map[string]any{
		"type": unicodenorm.Name,
		"form": unicodenorm.NFC,
	})
	if err != nil {
		return nil, err
	}

	err = mapping.AddCustomAnalyzer(pullIndexerAnalyzer, map[string]any{
		"type":          custom.Name,
		"char_filters":  []string{},
		"tokenizer":     unicode.Name,
		"token_filters": []string{unicodeNormalizeName, camelcase.Name, lowercase.Name},
	})
	if err != nil {
		return nil, err
	}

	mapping.DefaultAnalyzer = pullIndexerAnalyzer
	mapping.AddDocumentMapping(pullIndexerDocType, docMapping)
	mapping.AddDocumentMapping("_all", bleve.NewDocumentDisabledMapping())
	mapping.DefaultMapping = bleve.NewDocumentDisabledMapping()

	return mapping, nil
}

func (ix *Indexer) intialize(ctx context.Context) (bool, error) {
	if ix.indexer != nil {
		return false, errors.New("indexer is already initialized")
	}

	indexer, err := openIndexer(ctx, ix.path)
	if err != nil {
		return false, err
	}
	if indexer != nil {
		ix.indexer = indexer
		return true, nil
	}

	mapping, err := generatePullIndexMapping()
	if err != nil {
		return false, err
	}
	indexer, err = bleve.New(ix.path, mapping)
	if err != nil {
		return false, err
	}

	ix.indexer = indexer

	return false, nil
}

func openIndexer(ctx context.Context, path string) (bleve.Index, error) {
	l := tlog.FromContext(ctx)
	indexer, err := bleve.Open(path)
	if err != nil {
		if errors.Is(err, upsidedown.IncompatibleVersion) {
			l.Info("Indexer was built with a previous version of bleve, deleting and rebuilding")
			return nil, os.RemoveAll(path)
		}
		return nil, nil
	}
	return indexer, nil
}

func PopulateIndexer(ctx context.Context, ix *Indexer, e db.Execer) error {
	l := tlog.FromContext(ctx)

	pulls, err := db.GetPulls(e)
	if err != nil {
		return err
	}
	count := len(pulls)
	err = ix.Index(ctx, pulls...)
	if err != nil {
		return err
	}
	l.Info("pulls indexed", "count", count)
	return err
}

// pullData data stored and will be indexed
type pullData struct {
	ID     int64  `json:"id"`
	RepoAt string `json:"repo_at"`
	PullID int    `json:"pull_id"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`

	Comments []pullCommentData `json:"comments"`
}

func makePullData(pull *models.Pull) *pullData {
	return &pullData{
		ID:     int64(pull.ID),
		RepoAt: pull.RepoAt.String(),
		PullID: pull.PullId,
		Title:  pull.Title,
		Body:   pull.Body,
		State:  pull.State.String(),
	}
}

// Type returns the document type, for bleve's mapping.Classifier interface.
func (i *pullData) Type() string {
	return pullIndexerDocType
}

type pullCommentData struct {
	Body string `json:"body"`
}

type searchResult struct {
	Hits  []int64
	Total uint64
}

const maxBatchSize = 20

func (ix *Indexer) Index(ctx context.Context, pulls ...*models.Pull) error {
	batch := bleveutil.NewFlushingBatch(ix.indexer, maxBatchSize)
	for _, pull := range pulls {
		pullData := makePullData(pull)
		if err := batch.Index(base36.Encode(pullData.ID), pullData); err != nil {
			return err
		}
	}
	return batch.Flush()
}

func (ix *Indexer) Delete(ctx context.Context, pullID int64) error {
	return ix.indexer.Delete(base36.Encode(pullID))
}

// Search searches for pulls
func (ix *Indexer) Search(ctx context.Context, opts models.PullSearchOptions) (*searchResult, error) {
	var queries []query.Query

	// TODO(boltless): remove this after implementing pulls page pagination
	limit := opts.Page.Limit
	if limit == 0 {
		limit = 500
	}

	if opts.Keyword != "" {
		queries = append(queries, bleve.NewDisjunctionQuery(
			bleveutil.MatchAndQuery("title", opts.Keyword, pullIndexerAnalyzer, 0),
			bleveutil.MatchAndQuery("body", opts.Keyword, pullIndexerAnalyzer, 0),
		))
	}
	queries = append(queries, bleveutil.KeywordFieldQuery("repo_at", opts.RepoAt))
	queries = append(queries, bleveutil.KeywordFieldQuery("state", opts.State.String()))

	var indexerQuery query.Query = bleve.NewConjunctionQuery(queries...)
	searchReq := bleve.NewSearchRequestOptions(indexerQuery, limit, opts.Page.Offset, false)
	res, err := ix.indexer.SearchInContext(ctx, searchReq)
	if err != nil {
		return nil, nil
	}
	ret := &searchResult{
		Total: res.Total,
		Hits:  make([]int64, len(res.Hits)),
	}
	for i, hit := range res.Hits {
		id, err := base36.Decode(hit.ID)
		if err != nil {
			return nil, err
		}
		ret.Hits[i] = id
	}
	return ret, nil
}
