package types

// A nicer git tree representation.
type NiceTree struct {
	Name      string
	Mode      string
	Size      int64
	IsFile    bool
	IsSubtree bool
}
