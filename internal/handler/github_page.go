package handler

import (
	"bytes"
	_ "embed"
	"html/template"
	"net/http"
)

//go:embed github_page.html
var githubPageHTML string

var githubPageTemplate = template.Must(template.New("github-connection").Parse(githubPageHTML))

type githubPageData struct {
	Success                   bool
	Selecting                 bool
	InstallationURL, State    string
	Title, Description, Login string
}

func renderGitHubConnectionPage(w http.ResponseWriter, status int, data githubPageData) {
	privatePageHeaders(w)
	var body bytes.Buffer
	if err := githubPageTemplate.Execute(&body, data); err != nil {
		http.Error(w, "Bağlantı sayfası gösterilemiyor.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}
