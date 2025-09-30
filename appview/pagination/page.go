package pagination

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
