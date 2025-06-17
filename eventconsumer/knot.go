package eventconsumer

import (
	"fmt"
	"net/url"
)

type KnotSource struct {
	Knot string
}

func (k KnotSource) Key() string {
	return k.Knot
}

func (k KnotSource) Url(cursor int64, dev bool) (*url.URL, error) {
	scheme := "wss"
	if dev {
		scheme = "ws"
	}

	u, err := url.Parse(scheme + "://" + k.Knot + "/events")
	if err != nil {
		return nil, err
	}

	if cursor != 0 {
		query := url.Values{}
		query.Add("cursor", fmt.Sprintf("%d", cursor))
		u.RawQuery = query.Encode()
	}
	return u, nil
}

func NewKnotSource(knot string) KnotSource {
	return KnotSource{
		Knot: knot,
	}
}
