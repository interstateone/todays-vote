package main

import (
	"fmt"
	"github.com/daaku/go.httpgzip"
	"github.com/darkhelmet/env"
	"net/http"
)

func main() {
	port := env.IntDefault("PORT", 8080)
	panic(http.ListenAndServe(fmt.Sprintf(":%d", port), httpgzip.NewHandler(http.FileServer(http.Dir("public")))))
}
