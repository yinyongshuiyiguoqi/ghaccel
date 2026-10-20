//go:build windows

package gui

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/lxn/walk"
	d "github.com/lxn/walk/declarative"

	"github.com/yinyongshuiyiguoqi/ghaccel/internal/config"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/hosts"
	roundpkg "github.com/yinyongshuiyiguoqi/ghaccel/internal/round"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/state"
)

//go:embed icon.png
var iconPNG []byte

type App struct {
	cfg       config.Config
	ctrl      *Controller
	mw        *walk.MainWindow
	ni        *walk.NotifyIcon
	status    *walk.Label
	counts    *walk.Label
	table     *walk.TableView
	logView   *walk.TextEdit
	startBtn  *walk.PushButton
	stopBtn   *walk.PushButton
	autoStart *walk.CheckBox
	model     *domainModel
	settings  Settings
	quitting  bool
}

func Run() error {
	handle, ok := acquireSingleInstance()
	if !ok {
		showAlreadyRunning()
		return nil
	}
	defer releaseSingleInstance(handle)

	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	a := &App{cfg: cfg, model: &domainModel{}, settings: loadSettings()}
	a.loadInitialState()
	if err := a.build(); err != nil {
		return err
	}
	a.ctrl = NewController(cfg, Callbacks{
		OnRound:  a.onRound,
		OnStatus: a.onStatus,
		OnLog:    a.onLog,
	})
	if n := len(a.model.rows); n > 0 {
		a.counts.SetText(fmt.Sprintf("上次运行：%d 个域名已选路", n))
	}
	a.appendLog("就绪：点击“开始加速”选择最优 IP；关闭窗口会最小化到托盘。")
	if err := a.setupTray(); err != nil {
		return err
	}
	if a.settings.AutoStart {
		a.mw.Synchronize(func() { a.start() })
	}
	a.mw.Run()
	a.ctrl.Close()
	if a.ni != nil {
		a.ni.Dispose()
	}
	return nil
}

func (a *App) build() error {
	return d.MainWindow{
		AssignTo: &a.mw,
		Title:    "ghaccel · GitHub 加速器",
		Size:     d.Size{Width: 960, Height: 700},
		MinSize:  d.Size{Width: 780, Height: 560},
		Layout:   d.VBox{},
		Children: []d.Widget{
			d.Composite{
				Layout: d.HBox{},
				Children: []d.Widget{
					d.Label{Text: "状态"},
					d.Label{AssignTo: &a.status, Text: "已停止"},
					d.HSpacer{},
					d.Label{AssignTo: &a.counts, Text: "尚未运行"},
				},
			},
			d.Composite{
				Layout: d.HBox{},
				Children: []d.Widget{
					d.PushButton{AssignTo: &a.startBtn, Text: "开始加速", OnClicked: a.start},
					d.PushButton{AssignTo: &a.stopBtn, Text: "停止", Enabled: false, OnClicked: a.stop},
					d.PushButton{Text: "立即刷新", OnClicked: a.refresh},
					d.PushButton{Text: "恢复 hosts", OnClicked: a.restore},
					d.PushButton{Text: "打开日志", OnClicked: a.openLog},
					d.CheckBox{AssignTo: &a.autoStart, Text: "启动时自动加速", Checked: a.settings.AutoStart, OnCheckedChanged: a.toggleAutoStart},
					d.HSpacer{},
				},
			},
			d.TableView{
				AssignTo:         &a.table,
				AlternatingRowBG: true,
				MinSize:          d.Size{Height: 150},
				MaxSize:          d.Size{Height: 380},
				Columns: []d.TableViewColumn{
					{Title: "域名", Width: 200},
					{Title: "IP", Width: 110},
					{Title: "评分", Width: 60},
					{Title: "状态", Width: 50},
					{Title: "来源", Width: 110},
				},
				Model: a.model,
			},
			d.Label{Text: "运行日志"},
			d.TextEdit{
				AssignTo: &a.logView,
				ReadOnly: true,
				VScroll:  true,
				MinSize:  d.Size{Height: 100},
			},
		},
	}.Create()
}

func (a *App) setupTray() error {
	img, err := png.Decode(bytes.NewReader(iconPNG))
	if err != nil {
		return err
	}
	icon, err := walk.NewIconFromImage(img)
	if err != nil {
		return err
	}
	_ = a.mw.SetIcon(icon)

	ni, err := walk.NewNotifyIcon(a.mw)
	if err != nil {
		return err
	}
	a.ni = ni
	if err := ni.SetIcon(icon); err != nil {
		return err
	}
	if err := ni.SetToolTip("ghaccel · GitHub 加速器"); err != nil {
		return err
	}
	if err := ni.SetVisible(true); err != nil {
		return err
	}

	add := func(text string, fn func()) {
		action := walk.NewAction()
		_ = action.SetText(text)
		action.Triggered().Attach(fn)
		_ = ni.ContextMenu().Actions().Add(action)
	}
	add("显示主窗口", a.showWindow)
	add("开始加速", a.start)
	add("停止", a.stop)
	add("立即刷新", a.refresh)
	_ = ni.ContextMenu().Actions().Add(walk.NewSeparatorAction())
	add("退出", a.quit)

	ni.MessageClicked().Attach(a.showWindow)
	a.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if a.quitting {
			return
		}
		*canceled = true
		a.mw.Hide()
		_ = ni.ShowInfo("ghaccel", "已最小化到托盘，加速会在后台继续")
	})
	return nil
}

func (a *App) showWindow() {
	a.mw.Show()
	_ = a.mw.Activate()
}

func (a *App) start() {
	if err := a.ctrl.Start(); err != nil {
		if errors.Is(err, hosts.ErrNeedAdmin) {
			a.offerElevate(err)
			return
		}
		walk.MsgBox(a.mw, "无法启动", err.Error(), walk.MsgBoxIconError)
	}
}

func (a *App) stop() {
	_ = a.ctrl.Stop(false)
}

func (a *App) refresh() {
	a.ctrl.Refresh()
}

func (a *App) restore() {
	if walk.MsgBox(a.mw, "恢复 hosts",
		"将停止加速并从 hosts 中移除 ghaccel 托管块，继续？",
		walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
		return
	}
	if err := a.ctrl.Stop(true); err != nil {
		if errors.Is(err, hosts.ErrNeedAdmin) {
			a.offerElevate(err)
			return
		}
		walk.MsgBox(a.mw, "恢复失败", err.Error(), walk.MsgBoxIconError)
	}
}

func (a *App) openLog() {
	if a.cfg.Logging.File == "" {
		return
	}
	if _, err := os.Stat(a.cfg.Logging.File); err != nil {
		walk.MsgBox(a.mw, "日志", "还没有日志文件，先运行一轮加速。", walk.MsgBoxIconInformation)
		return
	}
	cmd := exec.Command("notepad.exe", a.cfg.Logging.File)
	_ = cmd.Start()
}

func (a *App) offerElevate(err error) {
	if walk.MsgBox(a.mw, "需要管理员权限",
		"写入 hosts 需要管理员权限。\n\n是否以管理员身份重新启动 ghaccel？",
		walk.MsgBoxYesNo|walk.MsgBoxIconWarning) != walk.DlgCmdYes {
		return
	}
	exe, e := os.Executable()
	if e != nil {
		walk.MsgBox(a.mw, "重启失败", e.Error(), walk.MsgBoxIconError)
		return
	}
	script := fmt.Sprintf("Start-Process -FilePath '%s' -Verb RunAs", exe)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-WindowStyle", "Hidden", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if e := cmd.Start(); e != nil {
		walk.MsgBox(a.mw, "重启失败", e.Error(), walk.MsgBoxIconError)
		return
	}
	a.quit()
}

func (a *App) toggleAutoStart() {
	a.settings.AutoStart = a.autoStart.Checked()
	if err := saveSettings(a.settings); err != nil {
		a.appendLog("! 保存设置失败: " + err.Error())
	}
}

func (a *App) quit() {
	a.quitting = true
	if a.ctrl != nil {
		a.ctrl.Stop(false)
	}
	if a.ni != nil {
		_ = a.ni.SetVisible(false)
	}
	a.mw.Close()
}

func (a *App) onRound(res roundpkg.Result) {
	a.mw.Synchronize(func() {
		a.model.SetRows(res.Domains)
	})
}

func (a *App) onStatus(s Status) {
	a.mw.Synchronize(func() {
		if s.Running {
			a.status.SetText("运行中")
		} else {
			a.status.SetText("已停止")
		}
		a.startBtn.SetEnabled(!s.Running)
		a.stopBtn.SetEnabled(s.Running)
		if s.Total > 0 {
			a.counts.SetText(fmt.Sprintf("可用 %d/%d · 降级 %d · 完成于 %s",
				s.Available, s.Total, s.Degraded, s.Last.Format("15:04:05")))
		}
		if s.Message != "" {
			a.appendLog("! " + s.Message)
		}
	})
}

func (a *App) onLog(line string) {
	a.mw.Synchronize(func() {
		a.appendLog(line)
	})
}

func (a *App) appendLog(line string) {
	if a.logView == nil {
		return
	}
	if a.logView.TextLength() > 400000 {
		_ = a.logView.SetText("")
	}
	a.logView.AppendText(line + "\r\n")
}

func (a *App) loadInitialState() {
	st, err := state.Load(a.cfg.Daemon.StateFile)
	if err != nil || len(st.Selected) == 0 {
		return
	}
	rows := make([]roundpkg.Outcome, 0, len(st.Selected))
	for d, s := range st.Selected {
		rows = append(rows, roundpkg.Outcome{
			Domain:   d,
			IP:       s.IP,
			Score:    s.Score,
			Sources:  s.Sources,
			Verified: s.Verified,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Domain < rows[j].Domain })
	a.model.rows = rows
}

type domainModel struct {
	walk.TableModelBase
	mu   sync.Mutex
	rows []roundpkg.Outcome
}

func (m *domainModel) RowCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rows)
}

func (m *domainModel) Value(row, col int) interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if row < 0 || row >= len(m.rows) {
		return ""
	}
	d := m.rows[row]
	switch col {
	case 0:
		return d.Domain
	case 1:
		if d.IP == "" {
			return "-"
		}
		return d.IP
	case 2:
		if d.IP == "" {
			return "-"
		}
		return fmt.Sprintf("%.0f", d.Score)
	case 3:
		if d.Degraded {
			return "降级"
		}
		if d.Verified {
			return "正常"
		}
		return "-"
	case 4:
		return strings.Join(d.Sources, ", ")
	}
	return ""
}

func (m *domainModel) SetRows(rows []roundpkg.Outcome) {
	cp := append([]roundpkg.Outcome(nil), rows...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Domain < cp[j].Domain })
	m.mu.Lock()
	m.rows = cp
	m.mu.Unlock()
	m.PublishRowsReset()
}
