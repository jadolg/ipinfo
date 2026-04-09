package main

import (
	"bytes"
	_ "embed"
	"html/template"
)

//go:embed index.html
var indexHTML string

var indexTmpl = template.Must(template.New("index").Parse(indexHTML))

type indexConfig struct {
	IPv4URL string
	IPv6URL string
}

func renderIndex(cfg indexConfig) []byte {
	var buf bytes.Buffer
	_ = indexTmpl.Execute(&buf, cfg)
	return buf.Bytes()
}
