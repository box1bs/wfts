package ui

import (
	"context"
	"fmt"
	"io"
	"time"
	"wfts/internal/model"

	"github.com/jroimartin/gocui"
)

const (
	minX = 		30
	minY = 		20
	maxLogSize =200

	metricsW = 	"metrics"
	logW = 		"log"
	inputW = 	"in"
	soutW = 	"out"
)

type UIManager struct {
	getCurrentState func() (int, error)
	searchFunc 		func(io.Writer, string, int) ([]*model.Document, []*model.DocRanking, *model.SearchMetrics)
	
	sresults 	[]*model.Document
	logLines 	[]string
	metrics 	*model.SearchMetrics
	lw 			*lw

	topLPSize, topRPSize, lpSize float64
}

func New(topLPSize, lpSize, topRPSize float64, logWriter *lw, getCurrentState func() (int, error), 
searchFunc func(io.Writer, string, int) ([]*model.Document, []*model.DocRanking, *model.SearchMetrics)) *UIManager {
	return &UIManager{
		topLPSize: topLPSize, topRPSize: topRPSize, lpSize: lpSize,
		getCurrentState: getCurrentState,
		searchFunc: searchFunc,
		lw: logWriter,
	}
}

type lw struct {
	logWriter chan []byte
}

func NewLogWriter(cap int) *lw {
	return &lw{logWriter: make(chan []byte, cap)}
}

func (lw *lw) Write(data []byte) (int, error) {
	select {
	case lw.logWriter <- data:
	default:
		<-lw.logWriter
		select {
		case lw.logWriter <- data:
		default:
		}
	}
	return len(data), nil
}

func (ui *UIManager) Run(cancel context.CancelFunc) error {
	gui, err := gocui.NewGui(gocui.OutputNormal)
	if err != nil {
		return err
	}
	defer gui.Close()

	gui.SetManagerFunc(ui.layout)
	gui.SetKeybinding(inputW, gocui.KeyEnter, gocui.ModNone, func(g *gocui.Gui, v *gocui.View) error {
		query := v.Buffer()
		return ui.search(g, query)
	})
	gui.SetKeybinding(soutW, gocui.KeyCtrl2, gocui.ModNone, ui.up)
	gui.SetKeybinding(soutW, gocui.KeyCtrl3, gocui.ModNone, ui.down)
	gui.SetKeybinding(logW, gocui.KeyCtrl4, gocui.ModNone, ui.up)
	gui.SetKeybinding(logW, gocui.KeyCtrl5, gocui.ModNone, ui.down)
	go func () {
		t := time.NewTicker(time.Second)
		for range t.C {
			gui.Update(ui.updateLogs)
		}
	}()
	go func () {
		t := time.NewTicker(10 * time.Second)
		for range t.C {
			gui.Update(ui.updateMetrics)
		}
	}()

	if err := gui.SetKeybinding("", gocui.KeyCtrlC, gocui.ModNone, func(g *gocui.Gui, v *gocui.View) error {
		cancel()
		return nil
	}); err != nil {
		return err
	}
	if err := gui.SetKeybinding("", gocui.KeyCtrlD, gocui.ModNone, quit); err != nil {
		return err
	}
	
	return gui.MainLoop()
}

func (ui *UIManager) renderLogs(view *gocui.View) {
	view.Clear()
	view.SetCursor(0, 0)
	view.SetOrigin(0, 0)
	ui.fetchLogs()

	if logSize := len(ui.logLines); logSize > maxLogSize {
		ui.logLines = ui.logLines[logSize - maxLogSize:]
	}
	for _, log := range ui.logLines {
		fmt.Fprint(view, log)
	}
}

func (ui *UIManager) up(g *gocui.Gui, v *gocui.View) error {
	_, oy := v.Origin()

	if oy > 0 {
		v.SetOrigin(0, oy - 1)
	}
	return nil
}

func (ui *UIManager) down(g *gocui.Gui, v *gocui.View) error {
	_, oy := v.Origin()
	_, sy := v.Size()

	curLen := len(v.BufferLines())

	if oy + sy < curLen {
		v.SetOrigin(0, oy + 1)
	}
	return nil
}

func (ui *UIManager) fetchLogs() {
	for {
		select {
		case log, ok := <-ui.lw.logWriter:
			if !ok {
				return
			}
			ui.logLines = append(ui.logLines, string(log))
		default:
			return
		}
	}
}

func (ui *UIManager) updateLogs(g *gocui.Gui) error {
	if v, err := g.View(logW); err == nil {
		ui.renderLogs(v)
	}
	return nil
}

func (ui *UIManager) search(g *gocui.Gui, query string) error {
	var metrics []*model.DocRanking
	ui.sresults, metrics, ui.metrics = ui.searchFunc(ui.lw, query, 100)
	metricsWriter, err := g.View(metricsW)
	if err != nil {
		return err
	}
	ui.renderMetrics(metricsWriter)
	resultsWriter, err := g.View(soutW)
	if err != nil {
		return err
	}
	ui.renderResults(ui.sresults, metrics, resultsWriter)
	return ui.updateMetrics(g)
}

func (ui *UIManager) renderResults(from []*model.Document, with []*model.DocRanking, to *gocui.View) {
	to.Clear()
	to.SetCursor(0, 0)
	to.SetOrigin(0, 0)

	for i, doc := range from {
		fmt.Fprintf(to, "%d: tokenCount: %d, tf idf: %.6f, bm25: %.6f, term proximity: %d\nlog length word in header: %f, has word in header: %t\nURL: %s\n", 
		i + 1, doc.TokenCount, with[i].Tf_Idf, with[i].BM25, with[i].TermProximity, with[i].LogLenWordInURL, with[i].HasWordInHeader, doc.URL)
	}
}

func (ui *UIManager) renderMetrics(view *gocui.View) {
	state, _ := ui.getCurrentState()
	view.Clear()
	if ui.metrics != nil {
		fmt.Fprintf(view, "QueryHandle: %d ms\n", ui.metrics.HandleQuery.Milliseconds())
		fmt.Fprintf(view, "ProcessAndFetch: %d ms\n", ui.metrics.FetchAndProcess.Milliseconds())
		fmt.Fprintf(view, "Sort: %d ms\n", ui.metrics.Sort.Milliseconds())
		fmt.Fprintf(view, "Total: %d ms\n", ui.metrics.Total.Milliseconds())
	}
	fmt.Fprintf(view, "Indexed: %d docs", state)
}

func (ui *UIManager) updateMetrics(g *gocui.Gui) error {
	if v, err := g.View(metricsW); err == nil {
		ui.renderMetrics(v)
	}
	return nil
}

func quit(*gocui.Gui, *gocui.View) error {
	return gocui.ErrQuit
}

func (ui *UIManager) layout(g *gocui.Gui) error {
	x, y := g.Size()
	if x < minX || y < minY {
		return fmt.Errorf("window is too small")
	}

	lps := float64(x) * ui.lpSize
	tlps := float64(y) * ui.topLPSize
	if v, err := g.SetView(metricsW, 0, 0, int(lps) - 1, int(tlps) - 1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Wrap = true
		v.Frame = true
		v.Title = "Metrics"
		ui.renderMetrics(v)
	}

	if v, err := g.SetView(logW, 0, int(tlps), int(lps) - 1, y - 1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Wrap = true
		v.Frame = true
		v.Autoscroll = true
		v.Title = "Logs"
	} else {
		ui.renderLogs(v)
	}

	trps := float64(y) * ui.topRPSize
	if v, err := g.SetView(inputW, int(lps), 0, x - 1, int(trps) - 1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Wrap = true
		v.Frame = true
		v.Editable = true
		v.Title = "Query"
		g.SetCurrentView(inputW)
	}

	if v, err := g.SetView(soutW, int(lps), int(trps), x - 1, y - 1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Wrap = true
		v.Frame = true
		v.Title = "Results"
	}
	return nil
}