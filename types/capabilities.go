package types

type Capabilities struct {
	PullRequests struct {
		FormatPatch       bool `json:"format_patch"`
		PatchSubmissions  bool `json:"patch_submissions"`
		BranchSubmissions bool `json:"branch_submissions"`
		ForkSubmissions   bool `json:"fork_submissions"`
	} `json:"pull_requests"`
}
