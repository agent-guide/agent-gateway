package model

import "errors"

var ErrNotFound = errors.New("not found")
var ErrReadOnly = errors.New("read-only")
var ErrConflict = errors.New("conflict")

type ServerRequest struct {
	ID     string   `json:"id"`
	Listen []string `json:"listen"`
	TLS    *TLSConf `json:"tls,omitempty"`
}

type TLSConf struct {
	Auto     bool   `json:"auto,omitempty"`
	CertFile string `json:"cert_file,omitempty"`
	KeyFile  string `json:"key_file,omitempty"`
}

type ServerResponse struct {
	ID        string          `json:"id"`
	Listen    []string        `json:"listen"`
	Routes    []RouteResponse `json:"routes,omitempty"`
	ReadOnly  bool            `json:"readonly,omitempty"`
	Source    string          `json:"source,omitempty"`
	PublicURL string          `json:"public_url,omitempty"`
}

type RouteRequest struct {
	ID       string        `json:"id"`
	Order    int           `json:"order"`
	Match    MatchConf     `json:"match"`
	Handlers []HandlerConf `json:"handlers"`
}

type MatchConf struct {
	Paths []string `json:"paths,omitempty"`
	Hosts []string `json:"hosts,omitempty"`
}

type HandlerConf struct {
	Type     string   `json:"type"`
	APIs     []string `json:"apis,omitempty"`
	Upstream string   `json:"upstream,omitempty"`
	Root     string   `json:"root,omitempty"`
}

type RouteResponse struct {
	ID       string        `json:"id"`
	Order    int           `json:"order"`
	Match    MatchConf     `json:"match"`
	Handlers []HandlerConf `json:"handlers"`
}
