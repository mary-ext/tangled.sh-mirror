package cursor

type Store interface {
	Set(knot string, cursor int64)
	Get(knot string) (cursor int64)
}
