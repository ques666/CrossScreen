//go:build darwin || windows

// Package tray hosts the cross-platform status-bar icon and its menu:
// open the config page, toggle login auto-start, and quit.
package tray

import (
	"embed"

	"crossscreen/platform/autostart"

	"fyne.io/systray"
)

//go:embed assets/tray-template.png assets/tray.ico
var iconFS embed.FS

// Options configures the tray menu.
type Options struct {
	PageURL    string
	OnOpenPage func()
	OnQuit     func()
}

// Run shows the tray icon and blocks until the user quits.
func Run(opts Options) {
	templatePNG, _ := iconFS.ReadFile("assets/tray-template.png")
	colorICO, _ := iconFS.ReadFile("assets/tray.ico")

	onReady := func() {
		systray.SetTooltip("跨屏助手 CrossScreen")
		// macOS uses the monochrome template glyph (auto-recolored for the
		// light/dark menu bar); Windows uses the full-color ICO.
		systray.SetTemplateIcon(templatePNG, colorICO)
		systray.SetTitle("")

		mOpen := systray.AddMenuItem("打开配置页面", "在浏览器中打开跨屏助手")
		systray.AddSeparator()
		mAuto := systray.AddMenuItemCheckbox("开机自启动", "", autostart.Enabled())
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("退出", "停止并退出跨屏助手")

		for {
			select {
			case <-mOpen.ClickedCh:
				if opts.OnOpenPage != nil {
					opts.OnOpenPage()
				}
			case <-mAuto.ClickedCh:
				if mAuto.Checked() {
					mAuto.Uncheck()
					_ = autostart.Disable()
				} else {
					if err := autostart.Enable(); err == nil {
						mAuto.Check()
					}
				}
			case <-mQuit.ClickedCh:
				if opts.OnQuit != nil {
					opts.OnQuit()
				}
				systray.Quit()
				return
			}
		}
	}
	systray.Run(onReady, nil)
}
