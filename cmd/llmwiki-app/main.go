// Command llmwiki-app is the LLM Wiki desktop product: a single pure-GUI binary
// that manages local knowledge bases, imports and chunks text documents, and
// answers questions with local retrieval plus an OpenAI-compatible endpoint.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"

	"ragflow-llmwiki/internal/gui"
)

// version is set at build time: -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v":
			fmt.Println("llmwiki-app " + version)
			return
		case "--help", "-h":
			fmt.Println("llmwiki-app " + version)
			fmt.Println("LLM Wiki — 本地知识库问答桌面应用。")
			fmt.Println("用法：llmwiki-app [-root <数据根目录>]")
			fmt.Println("  -root <path>   覆盖数据根目录（缺省自动选择可写位置）")
			return
		}
	}

	root := ""
	if len(os.Args) > 2 && os.Args[1] == "-root" {
		root = os.Args[2]
	}

	app, err := gui.New(root)
	if err != nil {
		msg := "LLM Wiki 启动失败：\n" + err.Error()
		fmt.Fprintln(os.Stderr, msg)
		showError("LLM Wiki", msg)
		os.Exit(1)
	}
	app.Setup()
	if err := runSafe(app); err != nil {
		fmt.Fprintln(os.Stderr, err)
		showError("LLM Wiki", "界面未能打开：\n"+err.Error())
		os.Exit(1)
	}
}

// runSafe wraps app.Run so an unexpected panic (for example during first-frame
// GPU draw) is written to a crash log beside the executable instead of silently
// flashing and exiting.
func runSafe(app *gui.App) (err error) {
	defer func() {
		if r := recover(); r != nil {
			logPanic(r)
			err = fmt.Errorf("界面崩溃: %v", r)
		}
	}()
	return app.Run()
}

func logPanic(r any) {
	msg := fmt.Sprintf("llmwiki-app panic: %v\n", r)
	fmt.Fprintln(os.Stderr, msg)
	if exe, perr := os.Executable(); perr == nil {
		_ = os.WriteFile(filepath.Join(filepath.Dir(exe), "llmwiki-crash.log"),
			[]byte(msg+string(debug.Stack())), 0o644)
	}
}
