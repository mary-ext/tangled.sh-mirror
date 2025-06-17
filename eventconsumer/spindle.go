package eventconsumer

import (
	"fmt"
	"net/url"
)

type SpindleSource struct {
	Spindle string
}

func (s SpindleSource) Key() string {
	return s.Spindle
}

func (s SpindleSource) Url(cursor int64, dev bool) (*url.URL, error) {
	scheme := "wss"
	if dev {
		scheme = "ws"
	}

	u, err := url.Parse(scheme + "://" + s.Spindle + "/events")
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

func NewSpindleSource(spindle string) SpindleSource {
	return SpindleSource{
		Spindle: spindle,
	}
}
