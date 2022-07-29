package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"strings"
)

// App struct
type App struct {
	ctx context.Context
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// Greet returns a greeting for the given name
func (a *App) GetPlgs() []string {
	//获取所有的插件
	plgs := filter("plugin", ".zip")
	for i, plg := range plgs {
		plgs[i] = strings.TrimSuffix(plg, ".zip")
	}
	fmt.Printf("plgs %#v\n", plgs)
	return plgs
}

func filter(dir, suffix string) []string {
	plgs := make([]string, 0)
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		log.Fatal(err)
		return plgs
	}
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), suffix) {
			continue
		}
		plgs = append(plgs, file.Name())
	}
	return plgs
}
