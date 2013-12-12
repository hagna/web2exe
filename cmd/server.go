package main

import (
	"flag"
    "time"
	"log"
	"net/http"
    "camlistore.org/ui"
)

func serveStaticFile(rw http.ResponseWriter, req *http.Request, root http.FileSystem, file string) {
	f, err := root.Open("/" + file)
	if err != nil {
		http.NotFound(rw, req)
		log.Printf("Failed to open file %q from uistatic.Files: %v", file, err)
		return
	}
	defer f.Close()
	var modTime time.Time
	if fi, err := f.Stat(); err == nil {
		modTime = fi.ModTime()
	}
	http.ServeContent(rw, req, file, modTime, f)
}

func f() func(w http.ResponseWriter, r *http.Request) {
	res := func(w http.ResponseWriter, r *http.Request) {
		//		log.Printf("Does %s have the same suffix as %s?  (I'll decide)", r.URL.Path, name)
        name := r.URL.Path
        log.Println(name)
        if name == "/" {
            name = "index.html"
        }
        serveStaticFile(w, r, ui.Files, name)
	}
	return res
}


func main() {
	flag.Parse()
    http.HandleFunc("/", f())
    log.Println("webui listening on :8093")
    http.ListenAndServe(":8093", nil)

}
