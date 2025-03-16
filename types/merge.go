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

type MergeRequest struct {
	Patch         string `json:"patch"`
	AuthorName    string `json:"authorName,omitempty"`
	AuthorEmail   string `json:"authorEmail,omitempty"`
	CommitBody    string `json:"commitBody,omitempty"`
	CommitMessage string `json:"commitMessage,omitempty"`
	Branch        string `json:"branch"`
}
