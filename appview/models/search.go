package models

import "tangled.org/core/appview/pagination"

type IssueSearchOptions struct {
	Keyword string
	RepoAt  string
	IsOpen  bool

	Page pagination.Page
}

// func (so *SearchOptions) ToFilters() []filter {
// 	var filters []filter
// 	if so.IsOpen != nil {
// 		openValue := 0
// 		if *so.IsOpen {
// 			openValue = 1
// 		}
// 		filters = append(filters, FilterEq("open", openValue))
// 	}
// 	return filters
// }
