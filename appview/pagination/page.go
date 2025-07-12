package pagination

import "context"

type Page struct {
	Offset int // where to start from
	Limit  int // number of items in a page
}

func FirstPage() Page {
	return Page{
		Offset: 0,
		Limit:  30,
	}
}

type ctxKey struct{}

func IntoContext(ctx context.Context, page Page) context.Context {
	return context.WithValue(ctx, ctxKey{}, page)
}

func FromContext(ctx context.Context) Page {
	if ctx == nil {
		return FirstPage()
	}
	v := ctx.Value(ctxKey{})
	if v == nil {
		return FirstPage()
	}
	page, ok := v.(Page)
	if !ok {
		return FirstPage()
	}
	return page
}

func (p Page) Previous() Page {
	if p.Offset-p.Limit < 0 {
		return FirstPage()
	} else {
		return Page{
			Offset: p.Offset - p.Limit,
			Limit:  p.Limit,
		}
	}
}

func (p Page) Next() Page {
	return Page{
		Offset: p.Offset + p.Limit,
		Limit:  p.Limit,
	}
}

func IterateAll[T any](
	fetch func(page Page) ([]T, error),
	handle func(items []T) error,
) error {
	page := FirstPage()
	for {
		items, err := fetch(page)
		if err != nil {
			return err
		}

		err = handle(items)
		if err != nil {
			return err
		}
		if len(items) < page.Limit {
			break
		}
		page = page.Next()
	}
	return nil
}
