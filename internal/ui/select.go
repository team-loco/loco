package ui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().
			Foreground(LocoWhite).
			Background(LocoGreen).
			Padding(0, 1)

	selectedStyle = lipgloss.NewStyle().
			Foreground(LocoGreen)
)

type selectItem struct {
	title       string
	description string
	value       any
}

func (i selectItem) Title() string       { return i.title }
func (i selectItem) Description() string { return i.description }
func (i selectItem) FilterValue() string { return i.title }

type selectModel struct {
	list     list.Model
	err      error
	selected any
}

func newSelectModel(title string, items []selectItem) selectModel {
	listItems := make([]list.Item, len(items))
	for i, item := range items {
		listItems[i] = item
	}

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = selectedStyle
	delegate.Styles.SelectedDesc = selectedStyle

	l := list.New(listItems, delegate, 0, 0)
	l.Title = title
	l.Styles.Title = titleStyle
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.SetShowHelp(false)

	return selectModel{list: l}
}

func (m selectModel) Init() tea.Cmd {
	return nil
}

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h, v := titleStyle.GetFrameSize()
		m.list.SetSize(msg.Width-h, msg.Height-v)

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			i, ok := m.list.SelectedItem().(selectItem)
			if ok {
				m.selected = i.value
			}
			return m, tea.Quit
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m selectModel) View() string {
	return m.list.View()
}

type SelectOption struct {
	Label       string
	Description string
	Value       any
}

func SelectFromList(title string, options []SelectOption) (any, error) {
	items := make([]selectItem, len(options))
	for i, opt := range options {
		items[i] = selectItem{
			title:       opt.Label,
			description: opt.Description,
			value:       opt.Value,
		}
	}

	m := newSelectModel(title, items)
	p := tea.NewProgram(m, tea.WithAltScreen())
	model, err := p.Run()
	if err != nil {
		return nil, err
	}

	sm, ok := model.(selectModel)
	if !ok {
		return nil, fmt.Errorf("internal error: unexpected model type")
	}

	if sm.selected == nil {
		return nil, fmt.Errorf("no selection made")
	}

	return sm.selected, sm.err
}
