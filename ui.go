package main

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"html/template"
	"net/http"
	"strings"

	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/css"
	"github.com/tdewolff/minify/v2/html"
	"github.com/tdewolff/minify/v2/js"
)

//go:embed index.html
var indexHTML string

var indexTmpl = template.Must(template.New("index").Parse(minifyIndex(indexHTML)))

func minifyIndex(src string) string {
	m := minify.New()
	m.Add("text/html", &html.Minifier{KeepQuotes: true})
	m.AddFunc("text/css", css.Minify)
	m.AddFunc("application/javascript", js.Minify)
	out, err := m.String("text/html", src)
	if err != nil {
		return src
	}
	return out
}

type indexConfig struct {
	IPv4URL string
	IPv6URL string
}

type renderedIndex struct {
	raw  []byte
	gzip []byte
}

func renderIndex(cfg indexConfig) renderedIndex {
	var buf bytes.Buffer
	_ = indexTmpl.Execute(&buf, cfg)
	raw := buf.Bytes()

	var gz bytes.Buffer
	gw, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	_, _ = gw.Write(raw)
	_ = gw.Close()

	return renderedIndex{raw: raw, gzip: gz.Bytes()}
}

func serveIndex(page renderedIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, "text/html; charset=utf-8")
		w.Header().Set("Vary", "Accept-Encoding")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(page.gzip)
			return
		}
		_, _ = w.Write(page.raw)
	}
}
