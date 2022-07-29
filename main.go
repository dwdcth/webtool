package main

import (
	"archive/zip"
	"embed"
	"fmt"
	"io/fs"
	"io/ioutil"
	"mime"
	"net/http"
	"os"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
)

//go:embed frontend/dist
var assets embed.FS

type FileLoader struct {
	http.Handler
}

func NewFileLoader() *FileLoader {
	return &FileLoader{}
}

type myCloser interface {
	Close() error
}

// closeFile is a helper function which streamlines closing
// with error checking on different file types.
func closeFile(f myCloser) {
	err := f.Close()
	check(err)
}
func readAll(file fs.File) []byte {
	// fc, err := file.Open()
	// check(err)
	// defer closeFile(fc)

	content, err := ioutil.ReadAll(file)
	check(err)

	return content
}

// check is a helper function which streamlines error checking
func check(e error) {
	if e != nil {
		panic(e)
	}
}
func (h *FileLoader) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	if strings.Contains(req.URL.Path, "chrome_tool") {
		res.Header().Set("Content-Type", "application/json")
		res.Write([]byte(`{"code":0,"data":[],"info":"操作成功"}`))
		return
	}
	requestedFilename := strings.TrimPrefix(req.URL.Path, "/")
	mimeString := mime.TypeByExtension(requestedFilename)
	if strings.Contains(requestedFilename, ".js") {
		mimeString = "application/javascript"
	}
	res.Header().Set("Content-Type", mimeString)
	if strings.Contains(req.URL.Path, "/plugin/") {
		paths := strings.Split(requestedFilename, "/")
		fmt.Printf("%#v\n", paths)
		pluginName := paths[1]
		filename := ""
		if len(paths) == 2 {
			filename = "index.html"
		} else {
			filename = strings.Join(paths[2:], "/")
		}
		requestedFilename = "./plugin/" + pluginName + ".zip"
		// 打开一个zip格式文件
		r, err := zip.OpenReader(requestedFilename)
		if err != nil {
			res.WriteHeader(http.StatusNotFound)
			res.Write([]byte(err.Error()))
			return
		}
		defer r.Close()
		fmt.Printf("filename %#v\n", filename)
		f, err := r.Open(filename)
		if err != nil {
			res.WriteHeader(http.StatusNotFound)
			res.Write([]byte(err.Error()))
			return
		}
		defer f.Close()
		res.Header().Set("Cache-Control", "max-age=604800")
		res.Write(readAll(f))
	} else {
		fileData, err := os.ReadFile(requestedFilename)
		if err != nil {

			res.WriteHeader(http.StatusNotFound)
			res.Write([]byte("404"))

			return
		}
		res.Write(fileData)
	}

}

func main() {
	// Create an instance of the app structure
	app := NewApp()

	// Create application with options
	err := wails.Run(&options.App{
		Title:            "网页工具箱",
		Width:            1024,
		Height:           768,
		Assets:           assets,
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		AssetsHandler:    NewFileLoader(),
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err)
	}
}
