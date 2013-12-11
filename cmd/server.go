package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
)

var passfile = flag.String("passfile", "passwords", "username:password list")
var simultaneous = flag.Int("sim", 1, "Simultaneous attacks")
var dev = flag.Bool("devmode", false, "Dev mode serves up")


func f() func(w http.ResponseWriter, r *http.Request) {
	res := func(w http.ResponseWriter, r *http.Request) {
		//		log.Printf("Does %s have the same suffix as %s?  (I'll decide)", r.URL.Path, name)
        name := r.URL.Path
        cwd, err := filepath.Abs(".")
        if err != nil {
            log.Fatal(err)
        }

        full := fmt.Sprintf("%s/%s", cwd, name)
        log.Printf("ServeFile: %s", full)
        http.ServeFile(w, r, full)
	}
	return res
}


func main() {
	flag.Parse()
    http.HandleFunc("/", f())
    log.Println("webui listening on :8093")
    http.ListenAndServe(":8093", nil)

}
