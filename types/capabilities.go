package types

type Capabilities struct {
	PullRequests struct {
		PatchSubmissions  bool `json:"patch_submissions"`
		BranchSubmissions bool `json:"branch_submissions"`
		ForkSubmissions   bool `json:"fork_submissions"`
	} `json:"pull_requests"`
}
