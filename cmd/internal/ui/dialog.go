package ui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

type YesNoModel struct {
	Question string
	Choice   string // "yes", "no", or ""
}

func NewYesNoModel(question string) YesNoModel {
	return YesNoModel{Question: question}
}

func (YesNoModel) Init() tea.Cmd {
	return nil
}

func (m YesNoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch msg.String() {
		case "y", "Y":
			m.Choice = "yes"
			return m, tea.Quit
		case "n", "N":
			m.Choice = "no"
			return m, tea.Quit
		case keyCtrlC, "q":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m YesNoModel) View() tea.View {
	if m.Choice == "" {
		return tea.NewView(fmt.Sprintf("%s (y/n): ", m.Question))
	}
	return tea.NewView("")
}

func AskYesNo(question string) (bool, error) {
	p := tea.NewProgram(NewYesNoModel(question))
	model, err := p.Run()
	if err != nil {
		return false, err
	}

	m, ok := model.(YesNoModel)
	if !ok {
		return false, errUnexpectedModel
	}
	return m.Choice == "yes", nil
}
