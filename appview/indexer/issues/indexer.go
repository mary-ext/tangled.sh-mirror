// heavily inspired by gitea's model (basically copy-pasted)
package issues_indexer

import (
	"context"
	"errors"
	"log"
	"os"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/upsidedown"
	"github.com/blevesearch/bleve/v2/search/query"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/indexer/base36"
	"tangled.org/core/appview/indexer/bleve"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/pagination"
	tlog "tangled.org/core/log"
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
		log.Fatalln("failed to initialize issue indexer", err)
	}
	if !existed {
		l.Debug("Populating the issue indexer")
		err := PopulateIndexer(ctx, ix, e)
		if err != nil {
			log.Fatalln("failed to populate issue indexer", err)
		}
	}
	l.Info("Initialized the issue indexer")
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

	mapping := bleve.NewIndexMapping()
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
	count := 0
	err := pagination.IterateAll(
		func(page pagination.Page) ([]models.Issue, error) {
			return db.GetIssuesPaginated(e, page)
		},
		func(issues []models.Issue) error {
			count += len(issues)
			return ix.Index(ctx, issues...)
		},
	)
	l.Info("issues indexed", "count", count)
	return err
}

// issueData data stored and will be indexed
type issueData struct {
	ID      int64  `json:"id"`
	RepoAt  string `json:"repo_at"`
	IssueID int    `json:"issue_id"`
	Title   string `json:"title"`
	Body    string `json:"body"`

	IsOpen   bool               `json:"is_open"`
	Comments []IssueCommentData `json:"comments"`
}

func makeIssueData(issue *models.Issue) *issueData {
	return &issueData{
		ID:      issue.Id,
		RepoAt:  issue.RepoAt.String(),
		IssueID: issue.IssueId,
		Title:   issue.Title,
		Body:    issue.Body,
		IsOpen:  issue.Open,
	}
}

type IssueCommentData struct {
	Body string `json:"body"`
}

type SearchResult struct {
	Hits  []int64
	Total uint64
}

const maxBatchSize = 20

func (ix *Indexer) Index(ctx context.Context, issues ...models.Issue) error {
	batch := bleveutil.NewFlushingBatch(ix.indexer, maxBatchSize)
	for _, issue := range issues {
		issueData := makeIssueData(&issue)
		if err := batch.Index(base36.Encode(issue.Id), issueData); err != nil {
			return err
		}
	}
	return batch.Flush()
}

// Search searches for issues
func (ix *Indexer) Search(ctx context.Context, opts models.IssueSearchOptions) (*SearchResult, error) {
	var queries []query.Query

	if opts.Keyword != "" {
		queries = append(queries, bleve.NewDisjunctionQuery(
			bleveutil.MatchAndQuery("title", opts.Keyword),
			bleveutil.MatchAndQuery("body", opts.Keyword),
		))
	}
	queries = append(queries, bleveutil.KeywordFieldQuery("repo_at", opts.RepoAt))
	queries = append(queries, bleveutil.BoolFieldQuery("is_open", opts.IsOpen))
	// TODO: append more queries

	var indexerQuery query.Query = bleve.NewConjunctionQuery(queries...)
	searchReq := bleve.NewSearchRequestOptions(indexerQuery, opts.Page.Limit, opts.Page.Offset, false)
	res, err := ix.indexer.SearchInContext(ctx, searchReq)
	if err != nil {
		return nil, nil
	}
	ret := &SearchResult{
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
