package gui

import (
	"context"
	"errors"
	"runtime/debug"
	"strings"

	"github.com/egoist/mygo/ui"

	"ragflow-llmwiki/internal/engine"
	"ragflow-llmwiki/internal/kbservice"
)

// settingsModal renders the OpenAI-compatible endpoint configuration form as a
// centered dialog. Edits are buffered in cfgDraft and persisted on "保存";
// "测试连接" probes the endpoint with a tiny embedding call.
func (a *App) settingsModal(c *ui.Context) {
	a.mu.Lock()
	draft := a.cfgDraft
	test := a.cfgTest
	a.mu.Unlock()
	t := c.Theme()

	ui.Modal(c, &a.settingsOpen, func() {
		ui.Column(c).Width(560).Gap(12).Children(func() {
			// Heading row.
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, "接口配置").FontSize(15).FontWeight(700)
				ui.Spacer(c)
				if ui.Button(c, "✕ 关闭").Clicked() {
					a.mu.Lock()
					a.settingsOpen = false
					a.mu.Unlock()
				}
			})
			ui.Text(c, "用于导入时的向量化与问答时的生成。填入 OpenAI 兼容接口（需含 /v1）。API Key 以明文保存在本机配置文件。").FontSize(11)

			ui.Column(c).Gap(8).Background(t.Surface).Border(1, t.Border).Radius(6).Padding(12).Children(func() {
				field(c, "服务地址（base URL，含 /v1）", func() { ui.TextInput(c, &draft.Server).FillWidth() })
				field(c, "API Key", func() { ui.TextInput(c, &draft.APIKey).FillWidth() })
				field(c, "对话模型（Chat model）", func() { ui.TextInput(c, &draft.ChatModel).FillWidth() })
				field(c, "向量模型（Embed model）", func() { ui.TextInput(c, &draft.EmbedModel).FillWidth() })

				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Toggle(c, &draft.Mock, "离线演示（mock）")
				})
				ui.Text(c, "勾选 mock 时，问答走纯本地检索证据，不发网络请求（离线可用）。").FontSize(10.5)
				if test != "" {
					ui.Text(c, test).FontSize(11).TextColor(t.TextMuted)
				}
			})

			// Footer actions.
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "测试连接").Clicked() {
					a.testConnection(c, draft)
				}
				if ui.PrimaryButton(c, "保存").Clicked() {
					draft = normalizeCfg(draft)
					if err := a.saveConfig(draft); err != nil {
						a.setStatusf("保存失败: %v", err)
					} else {
						a.setStatusf("配置已保存。")
						a.mu.Lock()
						a.settingsOpen = false
						a.mu.Unlock()
					}
				}
			})
		})
	})

	// Write draft changes back each frame (mirror binding back to host state).
	a.mu.Lock()
	a.cfgDraft = draft
	a.mu.Unlock()
}

func field(c *ui.Context, label string, body func()) {
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		ui.Text(c, label).FontSize(11.5)
		body()
	})
}

func normalizeCfg(cfg kbservice.Config) kbservice.Config {
	cfg.Server = strings.TrimSpace(cfg.Server)
	if cfg.Server != "" && !strings.HasSuffix(cfg.Server, "/v1") && !strings.HasSuffix(cfg.Server, "/v1/") {
		cfg.Server = cfg.Server + "/v1"
	}
	cfg.ChatModel = strings.TrimSpace(cfg.ChatModel)
	cfg.EmbedModel = strings.TrimSpace(cfg.EmbedModel)
	return cfg
}

// testConnection performs a lightweight embedding call to validate the endpoint.
func (a *App) testConnection(c *ui.Context, draft kbservice.Config) {
	if draft.Mock {
		a.mu.Lock()
		a.cfgTest = "mock 模式：无需连接测试。"
		a.mu.Unlock()
		return
	}
	if draft.Server == "" {
		a.mu.Lock()
		a.cfgTest = "请先填写服务地址。"
		a.mu.Unlock()
		return
	}
	a.setStatusf("正在测试连接…")
	eng := engine.NewOpenAI(engine.Config{
		BaseURL:    draft.Server,
		APIKey:     draft.APIKey,
		ChatModel:  draft.ChatModel,
		EmbedModel: draft.EmbedModel,
	})
	go func() {
		msg := a.runTestConnection(eng)
		a.mu.Lock()
		a.cfgTest = msg
		a.mu.Unlock()
		a.setStatusf("测试连接结束。")
	}()
}

// runTestConnection performs an embedding call and returns the human-readable
// result. It runs off the render thread; a panic inside the probe (recovered
// here) must not take down the whole process — the app should surface a message
// instead of vanishing.
func (a *App) runTestConnection(eng *engine.OpenAI) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			logCrashf("测试连接 panic: %v\n%s", r, debug.Stack())
			msg = "连接过程出现异常（详见崩溃日志）。"
		}
	}()
	_, err := eng.Encode(context.Background(), []string{"ping"})
	switch {
	case err == nil:
		return "连接成功 ✓ 地址与密钥有效。"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "测试未完成：请求被取消或超时。"
	default:
		return "连接失败：" + err.Error()
	}
}

func (a *App) saveConfig(cfg kbservice.Config) error {
	if err := kbservice.SaveConfig(a.root, cfg); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
	return nil
}
