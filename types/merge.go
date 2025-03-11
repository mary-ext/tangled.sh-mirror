package types

type ConflictInfo struct {
	Filename string `json:"filename"`
	Reason   string `json:"reason"`
}

type MergeCheckResponse struct {
	IsConflicted bool           `json:"is_conflicted"`
	Conflicts    []ConflictInfo `json:"conflicts"`
	Message      string         `json:"message"`
	Error        string         `json:"error"`
}
