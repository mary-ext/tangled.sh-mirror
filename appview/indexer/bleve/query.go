package bleveutil

import (
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
)

func MatchAndQuery(field, keyword string) query.Query {
	q := bleve.NewMatchQuery(keyword)
	q.FieldVal = field
	return q
}

func BoolFieldQuery(field string, val bool) query.Query {
	q := bleve.NewBoolFieldQuery(val)
	q.FieldVal = field
	return q
}

func KeywordFieldQuery(field, keyword string) query.Query {
	q := bleve.NewTermQuery(keyword)
	q.FieldVal = field
	return q
}
