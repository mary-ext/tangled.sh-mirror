package types

// A nicer git tree representation.
type NiceTree struct {
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	Size      int64  `json:"size"`
	IsFile    bool   `json:"is_file"`
	IsSubtree bool   `json:"is_subtree"`
}
