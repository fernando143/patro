package tui

import "github.com/charmbracelet/huh"

// SynthwaveHuhTheme returns a huh form theme in patro's 80s-synthwave
// palette: neon magenta titles, cyan cursors, mint selections over the same
// colors the dashboard uses.
func SynthwaveHuhTheme() *huh.Theme {
	t := huh.ThemeBase()

	f := &t.Focused
	f.Base = f.Base.BorderForeground(colorMagenta)
	f.Title = f.Title.Foreground(colorMagenta).Bold(true)
	f.NoteTitle = f.NoteTitle.Foreground(colorMagenta).Bold(true)
	f.Description = f.Description.Foreground(colorDim)
	f.SelectSelector = f.SelectSelector.Foreground(colorCyan)
	f.SelectedOption = f.SelectedOption.Foreground(colorGreen).Bold(true)
	f.SelectedPrefix = f.SelectedPrefix.Foreground(colorGreen)
	f.UnselectedOption = f.UnselectedOption.Foreground(colorText)
	f.FocusedButton = f.FocusedButton.Background(colorMagenta).Foreground(colorBg).Bold(true)
	f.BlurredButton = f.BlurredButton.Foreground(colorDim)
	f.ErrorIndicator = f.ErrorIndicator.Foreground(colorRed)
	f.ErrorMessage = f.ErrorMessage.Foreground(colorRed)
	f.TextInput.Cursor = f.TextInput.Cursor.Foreground(colorCyan)
	f.TextInput.Prompt = f.TextInput.Prompt.Foreground(colorPurple)
	f.TextInput.Placeholder = f.TextInput.Placeholder.Foreground(colorDim)
	f.TextInput.Text = f.TextInput.Text.Foreground(colorText)

	// Blurred groups mirror the focused styling but with a muted border.
	t.Blurred = t.Focused
	t.Blurred.Base = t.Blurred.Base.BorderForeground(colorDim)
	t.Blurred.Title = t.Blurred.Title.Foreground(colorDim)

	t.Help.ShortKey = t.Help.ShortKey.Foreground(colorCyan)
	t.Help.ShortDesc = t.Help.ShortDesc.Foreground(colorDim)
	return t
}
