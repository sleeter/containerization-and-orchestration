package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
)

var (
	mu   sync.Mutex
	hold [][]byte // удерживаем выделенную память, чтобы GC её не вернул
)

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})

	// /eat?mb=N — выделяет N МБ и держит их. Страницы трогаем явно,
	// иначе ядро не закоммитит память и RSS не вырастет.
	http.HandleFunc("/eat", func(w http.ResponseWriter, r *http.Request) {
		mb, err := strconv.Atoi(r.URL.Query().Get("mb"))
		if err != nil || mb <= 0 {
			http.Error(w, "usage: /eat?mb=N", http.StatusBadRequest)
			return
		}
		buf := make([]byte, mb<<20)
		for i := 0; i < len(buf); i += 4096 {
			buf[i] = 1
		}
		mu.Lock()
		hold = append(hold, buf)
		total := len(hold)
		mu.Unlock()
		fmt.Fprintf(w, "allocated %d MB (chunks held: %d)\n", mb, total)
	})

	// /burn — крутит одно ядро в бесконечном цикле.
	http.HandleFunc("/burn", func(w http.ResponseWriter, r *http.Request) {
		go func() {
			var x uint64
			for {
				x++
			}
		}()
		fmt.Fprintln(w, "burning one core")
	})

	log.Printf("api listening on %s (pid %d)", addr, os.Getpid())
	log.Fatal(http.ListenAndServe(addr, nil))
}
