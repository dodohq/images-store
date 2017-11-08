package main

import (
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/schema"
	"github.com/graymeta/stow"
	"github.com/graymeta/stow/s3"
	"github.com/joho/godotenv"
)

var container stow.Container

type reqBody struct {
	Filetype string `schema:"filetype"`
}

func main() {
	isDevEnv := os.Getenv("GO_ENV") == "development"
	if isDevEnv {
		if err := godotenv.Load(); err != nil {
			log.Fatal(err)
		}
	}

	location, err := stow.Dial("s3", stow.ConfigMap{
		s3.ConfigAccessKeyID: os.Getenv("AWS_ACCESS_KEY"),
		s3.ConfigSecretKey:   os.Getenv("AWS_SECRET_KEY"),
		s3.ConfigRegion:      os.Getenv("AWS_REGION"),
	})
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
	container, err = location.Container(os.Getenv("AWS_BUCKET"))
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}

	http.HandleFunc("/", httpRespond)
	http.HandleFunc("/image", getImage)

	whereToStart := ":" + os.Getenv("PORT")
	if isDevEnv {
		whereToStart = "localhost" + whereToStart
	}
	fmt.Println("Starting server at", whereToStart)
	log.Fatal(http.ListenAndServe(whereToStart, nil))
}

func httpRespond(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		// authorize upload
		ok := r.Header.Get("Authorization") == os.Getenv("AUTH_KEY")
		if !ok {
			http.Error(w, "unauthorized", 401)
			return
		}

		contentType := r.Header.Get("Content-Type")
		if !strings.Contains(contentType, "multipart/form-data") {
			http.Error(w, "Content-Type not accepted", 400)
			return
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		// Handle meta data of image
		decoder := schema.NewDecoder()
		decoder.IgnoreUnknownKeys(true)
		var data reqBody
		if err := decoder.Decode(&data, r.PostForm); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		// Handle the actual image
		file, fileHeader, err := r.FormFile("image")
		defer file.Close()
		if file == nil {
			http.Error(w, "no image", 400)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		_, err = container.Put(strconv.FormatInt(time.Now().Unix(), 10)+"."+data.Filetype, file, fileHeader.Size, nil)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Write([]byte("OK"))
	} else if r.Method == "GET" {
		cursorList, ok := r.URL.Query()["cursor"]
		var cursor string
		if !ok || len(cursorList) < 1 {
			cursor = stow.CursorStart
		} else {
			cursor = cursorList[0]
		}

		items, nextCursor, err := container.Items(stow.NoPrefix, cursor, 100)
		tmpl, err := template.New("index.tmpl").ParseFiles("index.tmpl")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		err = tmpl.Execute(w, struct {
			Items      []stow.Item
			NextCursor string
		}{items, nextCursor})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
}

func getImage(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		imageIDList, ok := r.URL.Query()["image"]
		if !ok || len(imageIDList) < 1 {
			http.Error(w, "missing image id", 400)
			return
		}

		imageID := imageIDList[0]
		item, err := container.Item(imageID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		reader, err := item.Open()
		if err != nil {
			http.Error(w, err.Error(), 500)
		}
		io.Copy(w, reader)
	} else {
		http.Error(w, "method not supported", 405)
	}
}
