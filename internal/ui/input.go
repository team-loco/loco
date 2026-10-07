package ui

import (
	"fmt"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

type inputModel struct {
	err       error
	textInput textinput.Model
}

func NewInputModel(prompt string) inputModel {
	ti := textinput.New()
	ti.Prompt = prompt
	ti.Focus()
	return inputModel{
		textInput: ti,
	}
}

func (inputModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m inputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch msg.String() {
		case "enter", keyCtrlC, "esc":
			return m, tea.Quit
		}
	}

	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m inputModel) View() tea.View {
	return tea.NewView(fmt.Sprintf(
		"%s\n\n%s",
		m.textInput.View(),
		"(esc to quit)",
	))
}

func AskForString(prompt string) (string, error) {
	p := tea.NewProgram(NewInputModel(prompt))
	model, err := p.Run()
	if err != nil {
		return "", err
	}

	m, ok := model.(inputModel)
	if !ok {
		return "", errUnexpectedModel
	}
	return m.textInput.Value(), m.err
}
