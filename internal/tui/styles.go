package tui

import "github.com/charmbracelet/lipgloss"

// Colors use standard ANSI 16 palette so they match the CLI/interactive
// mode output and respect the user's terminal color scheme.
var (
	colorRed     = lipgloss.ANSIColor(1)
	colorGreen   = lipgloss.ANSIColor(2)
	colorYellow  = lipgloss.ANSIColor(3)
	colorMagenta = lipgloss.ANSIColor(5)
	colorCyan    = lipgloss.ANSIColor(6)
	colorDim     = lipgloss.ANSIColor(8) // bright black / gray

	// List view
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Reverse(true).
			Padding(0, 1)

	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorMagenta)

	normalStyle = lipgloss.NewStyle()

	dimStyle = lipgloss.NewStyle().Foreground(colorDim)

	resolvedBadge = lipgloss.NewStyle().
			Foreground(colorGreen).
			Render("✓ resolved")

	openBadge = lipgloss.NewStyle().
			Foreground(colorYellow).
			Render("● open")

	// Detail view
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Reverse(true).
			Padding(0, 1).
			MarginBottom(1)

	authorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorGreen)

	botStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorYellow)

	dateStyle = lipgloss.NewStyle().Foreground(colorDim)

	diffAddStyle     = lipgloss.NewStyle().Foreground(colorGreen)
	diffRemoveStyle  = lipgloss.NewStyle().Foreground(colorRed)
	diffHunkStyle    = lipgloss.NewStyle().Foreground(colorCyan)
	diffContextStyle = lipgloss.NewStyle().Foreground(colorDim)

	codeBlockStyle  = lipgloss.NewStyle().Foreground(colorCyan)
	inlineCodeStyle = lipgloss.NewStyle().Foreground(colorMagenta)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(colorDim).
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(colorDim)
)
