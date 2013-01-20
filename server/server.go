package main

import (
	"github.com/daaku/go.httpgzip"
	"net/http"
)

func main() {
	panic(http.ListenAndServe(":8080", httpgzip.NewHandler(http.FileServer(http.Dir("public")))))
}
