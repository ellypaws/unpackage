package components

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/ellypaws/unpackage/pkg/session"
)

const gradientSteps = 64

var gradientRamp = buildGradient(gradientSteps, tone{212, .32, .92}, tone{270, .24, .88})
var Accent = GradientColor(.68)
var Cyan = GradientColor(.08)
var Pink = GradientColor(.92)
var Green = lipgloss.Color("#7ED6A5")
var Muted = lipgloss.Color("#A4A6B5")
var MutedStyle = lipgloss.NewStyle().Foreground(Muted)
var Warn = lipgloss.NewStyle().Foreground(lipgloss.Color("#E8BE79"))
var Deleted = lipgloss.Color("#F7768E")
var GroupDM = lipgloss.Color("#E8BE79")
var Title = lipgloss.NewStyle().Bold(true).Foreground(Accent)
var Border = Brightness(Saturation(GradientColor(.56), -.12), -.48)
var Surface = lipgloss.Color("#252331")
var SurfaceHover = lipgloss.Color("#303044")
var Text = lipgloss.Color("#E1DDEB")

var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

var adjustedColors sync.Map
var gradientText sync.Map
var shimmerText sync.Map
var separatorText sync.Map

func Fit(s string, w int) string { return ansi.Truncate(session.Safe(s), max(1, w), "…") }
func FitStyled(s string, w int) string {
	return ansi.Truncate(s, max(1, w), "…")
}
func DisabledButton(label string) string {
	return lipgloss.NewStyle().Foreground(Border).Padding(0, 1).Render(label)
}

func InputField(z *zone.Manager, id string, input *textinput.Model, width int, hover, focus, action, hint string) string {
	contentWidth := max(1, width-4)
	textWidth := max(1, contentWidth-lipgloss.Width(action))
	if input.Position() != len([]rune(input.Value())) {
		hint = ""
	}
	if hint != "" {
		hint = " " + Fit(hint, max(1, textWidth/2-1))
	}
	inputWidth := max(1, textWidth-lipgloss.Width(hint))
	input.Width = max(1, inputWidth-lipgloss.Width(input.Prompt)-1)
	input.SetCursor(input.Position())
	border := Border
	if hover == id || focus == id {
		border = Accent
	}
	body := ansi.Truncate(input.View(), inputWidth, "")
	body = lipgloss.NewStyle().Width(inputWidth).Render(body) + MutedStyle.Render(hint) + action
	return z.Mark(id, lipgloss.NewStyle().Width(width-2).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(border).Render(body))
}

func Button(z *zone.Manager, id, label, hover, focus string, active bool) string {
	st := lipgloss.NewStyle().Foreground(Text).Background(Surface).Padding(0, 1)
	prefix := ""
	if active {
		st = st.Foreground(Accent).Bold(true)
		if os.Getenv("NO_COLOR") != "" {
			prefix = "*"
		}
	}
	if id == hover || id == focus {
		st = st.Foreground(lipgloss.Color("#FFFFFF")).Background(SurfaceHover).Underline(true).Bold(true)
		if os.Getenv("NO_COLOR") != "" {
			prefix = ">"
		}
	}
	return z.Mark(id, st.Render(prefix+label))
}

func Tab(z *zone.Manager, id, label, hover, focus string, active bool, padding int) string {
	b := lipgloss.Border{Top: "─", Bottom: "─", Left: "│", Right: "│", TopLeft: "╭", TopRight: "╮", BottomLeft: "┴", BottomRight: "┴"}
	style := lipgloss.NewStyle().Border(b).BorderForeground(Border).Foreground(Muted).Padding(0, padding)
	if active {
		b.Bottom = " "
		b.BottomLeft = "┘"
		b.BottomRight = "└"
		style = style.Border(b).Foreground(Accent).Bold(true)
	}
	if hover == id || focus == id {
		style = style.Foreground(Pink).BorderForeground(Accent).Underline(true).Bold(true)
		if os.Getenv("NO_COLOR") != "" {
			label = ">" + label
		}
	}
	return z.Mark(id, style.Render(label))
}
func Rule(frac float64, w int) string {
	return progress(frac, w, 0, false)
}

func Progress(frac float64, w, frame int) string {
	return progress(frac, w, frame, true)
}

func progress(frac float64, w, frame int, moving bool) string {
	w = max(0, w)
	n := float64(w) * max(0, min(1, frac))
	sweep := frame%(w+6) - 3
	var b strings.Builder
	for i := 0; i < w; i++ {
		glyph := "─"
		color := Brightness(Saturation(GradientColor(float64(i)/float64(max(1, w-1))), -.16), -.58)
		if float64(i) < n {
			glyph = "━"
			color = GradientColor(float64(i) / float64(max(1, w-1)))
			distance := i - sweep
			if distance < 0 {
				distance = -distance
			}
			if moving && distance <= 1 {
				color = Brightness(Saturation(color, -.08), .08)
			}
		} else if moving {
			color = Brightness(color, -.04*float64(i-int(n)))
		}
		if os.Getenv("NO_COLOR") != "" {
			b.WriteString(glyph)
		} else {
			b.WriteString(lipgloss.NewStyle().Foreground(color).Render(glyph))
		}
	}
	return b.String()
}

func Indeterminate(w, frame int) string {
	w = max(0, w)
	if w == 0 {
		return ""
	}
	center := frame%(w+4) - 2
	var b strings.Builder
	for i := range w {
		distance := i - center
		if distance < 0 {
			distance = -distance
		}
		glyph := "─"
		color := Brightness(Saturation(GradientColor(float64(i)/float64(max(1, w-1))), -.16), -.58)
		if distance <= 1 {
			glyph = "━"
			color = Brightness(GradientColor(float64(i)/float64(max(1, w-1))), .06-float64(distance)*.04)
		}
		if os.Getenv("NO_COLOR") != "" {
			b.WriteString(glyph)
		} else {
			b.WriteString(lipgloss.NewStyle().Foreground(color).Render(glyph))
		}
	}
	return b.String()
}

func Spinner(frame int) string {
	glyph := string(spinnerFrames[frame%len(spinnerFrames)])
	if os.Getenv("NO_COLOR") != "" {
		return glyph
	}
	position := float64(frame%12) / 11
	return lipgloss.NewStyle().Foreground(GradientColor(position)).Bold(true).Render(glyph)
}

func Working(label string, frame, width int) string {
	labelWidth := max(1, width-16)
	label = Fit(label, labelWidth)
	line := Spinner(frame) + " " + Shimmer(label, frame)
	barWidth := min(10, max(0, width-lipgloss.Width(line)-1))
	if barWidth > 0 {
		line += " " + Indeterminate(barWidth, frame)
	}
	return FitStyled(line, width)
}

func Shimmer(text string, frame int) string {
	if os.Getenv("NO_COLOR") != "" {
		return text
	}
	runes := []rune(text)
	position := frame%(len(runes)+8) - 4
	key := struct {
		Text     string
		Position int
	}{text, position}
	if cached, ok := shimmerText.Load(key); ok {
		return cached.(string)
	}
	base := Brightness(Saturation(GradientColor(.52), -.14), -.18)
	var b strings.Builder
	for i, r := range runes {
		if r == ' ' {
			b.WriteRune(r)
			continue
		}
		distance := i - position
		if distance < 0 {
			distance = -distance
		}
		color := base
		if distance <= 3 {
			color = Brightness(Saturation(base, -.05), []float64{.27, .18, .10, .04}[distance])
		}
		b.WriteString(lipgloss.NewStyle().Foreground(color).Render(string(r)))
	}
	result := b.String()
	shimmerText.Store(key, result)
	return result
}

func Gradient(text string) string {
	if os.Getenv("NO_COLOR") != "" {
		return lipgloss.NewStyle().Bold(true).Render(text)
	}
	if cached, ok := gradientText.Load(text); ok {
		return cached.(string)
	}
	var b strings.Builder
	runes := []rune(text)
	for i, r := range runes {
		if r == ' ' {
			b.WriteRune(r)
			continue
		}
		b.WriteString(lipgloss.NewStyle().Foreground(GradientColor(float64(i) / float64(max(1, len(runes)-1)))).Bold(true).Render(string(r)))
	}
	result := b.String()
	gradientText.Store(text, result)
	return result
}

func GradientColor(position float64) lipgloss.Color {
	position = max(0, min(1, position))
	return gradientRamp[int(position*float64(len(gradientRamp)-1)+.5)]
}

// Palettes are single-hue sequential ramps from dim to bright for magnitude encodings.
var Palettes = []string{"violet", "amber", "green", "cyan"}

var paletteRamps = map[string][]lipgloss.Color{
	"violet": buildRamp(gradientSteps, tone{262, .48, .34}, tone{258, .42, .66}, tone{248, .28, .98}),
	"amber":  buildRamp(gradientSteps, tone{28, .62, .34}, tone{36, .78, .72}, tone{46, .52, 1}),
	"green":  buildRamp(gradientSteps, tone{152, .55, .30}, tone{150, .58, .64}, tone{140, .40, .96}),
	"cyan":   buildRamp(gradientSteps, tone{202, .55, .32}, tone{198, .58, .70}, tone{188, .34, .98}),
}

func PaletteColor(name string, position float64) lipgloss.Color {
	ramp := paletteRamps[name]
	if ramp == nil {
		ramp = paletteRamps[Palettes[0]]
	}
	position = max(0, min(1, position))
	return ramp[int(position*float64(len(ramp)-1)+.5)]
}

func buildRamp(steps int, stops ...tone) []lipgloss.Color {
	if len(stops) < 2 {
		return buildGradient(steps, stops[0], stops[0])
	}
	colors := make([]lipgloss.Color, 0, steps)
	segments := len(stops) - 1
	for i := range steps {
		position := float64(i) / float64(max(1, steps-1)) * float64(segments)
		segment := min(segments-1, int(position))
		local := position - float64(segment)
		start, end := stops[segment], stops[segment+1]
		current := tone{h: start.h + (end.h-start.h)*local, s: start.s + (end.s-start.s)*local, v: start.v + (end.v-start.v)*local}
		colors = append(colors, lipgloss.Color(current.rgb().hex()))
	}
	return colors
}

func Brightness(color lipgloss.Color, amount float64) lipgloss.Color {
	return adjust(color, 0, amount)
}

func Saturation(color lipgloss.Color, amount float64) lipgloss.Color {
	return adjust(color, amount, 0)
}

func Fade(color lipgloss.Color, distance int) lipgloss.Color {
	if distance < 0 {
		return color
	}
	distance = min(5, distance)
	brightness := []float64{.06, -.13, -.25, -.36, -.45, -.52}[distance]
	saturation := []float64{.04, -.03, -.07, -.11, -.14, -.16}[distance]
	return Brightness(Saturation(color, saturation), brightness)
}

func Separator(width int) string {
	width = max(0, width)
	if os.Getenv("NO_COLOR") != "" {
		return strings.Repeat("─", width)
	}
	if cached, ok := separatorText.Load(width); ok {
		return cached.(string)
	}
	var b strings.Builder
	for i := range width {
		color := Brightness(Saturation(GradientColor(float64(i)/float64(max(1, width-1))), -.16), -.52)
		b.WriteString(lipgloss.NewStyle().Foreground(color).Render("─"))
	}
	result := b.String()
	separatorText.Store(width, result)
	return result
}

func TitleRule(title string, width, frame int, active bool) string {
	title = Fit(title, max(1, width-5))
	left := Separator(min(2, width))
	if width <= 3 {
		return left
	}
	styledTitle := Gradient(title)
	if active {
		styledTitle = Shimmer(title, frame)
	}
	used := lipgloss.Width(left) + 2 + lipgloss.Width(styledTitle)
	right := Separator(max(0, width-used))
	return FitStyled(left+" "+styledTitle+" "+right, width)
}

func TitledBox(title, body string, width, padding int, border lipgloss.Border, color, titleColor lipgloss.Color, frame int, active bool) string {
	width = max(6, width)
	padding = max(0, padding)
	contentWidth := max(1, width-2-padding*2)
	title = Fit(title, max(1, width-7))
	styledTitle := Gradient(title)
	if titleColor != "" {
		styledTitle = lipgloss.NewStyle().Foreground(titleColor).Bold(true).Render(title)
	} else if active {
		styledTitle = Shimmer(title, frame)
	}

	topWidth := width - 2
	leftWidth := min(2, max(1, topWidth-lipgloss.Width(styledTitle)-2))
	rightWidth := max(0, topWidth-leftWidth-lipgloss.Width(styledTitle)-2)
	top := borderCell(border.TopLeft, color) + borderRun(border.Top, leftWidth, color, 0, .12) + " " + styledTitle + " " + borderRun(border.Top, rightWidth, color, .12, 1) + borderCell(border.TopRight, color)

	lines := strings.Split(body, "\n")
	var out strings.Builder
	out.WriteString(top)
	for i, line := range lines {
		if lipgloss.Width(line) > contentWidth {
			line = ansi.Truncate(line, contentWidth, "")
		}
		line = lipgloss.NewStyle().Width(contentWidth).Render(line)
		position := float64(i+1) / float64(len(lines)+1)
		side := Brightness(Saturation(color, -.04*position), -.12*position)
		out.WriteByte('\n')
		out.WriteString(borderCell(border.Left, side))
		out.WriteString(strings.Repeat(" ", padding))
		out.WriteString(line)
		out.WriteString(strings.Repeat(" ", padding))
		out.WriteString(borderCell(border.Right, side))
	}
	bottom := Brightness(Saturation(color, -.04), -.12)
	out.WriteByte('\n')
	out.WriteString(borderCell(border.BottomLeft, bottom))
	out.WriteString(borderRun(border.Bottom, width-2, bottom, 0, 1))
	out.WriteString(borderCell(border.BottomRight, bottom))
	return out.String()
}

func borderRun(glyph string, width int, color lipgloss.Color, start, end float64) string {
	if os.Getenv("NO_COLOR") != "" {
		return strings.Repeat(glyph, width)
	}
	var b strings.Builder
	for i := range width {
		position := start + (end-start)*float64(i)/float64(max(1, width-1))
		b.WriteString(borderCell(glyph, Brightness(Saturation(color, -.03*position), -.06*position)))
	}
	return b.String()
}

func borderCell(glyph string, color lipgloss.Color) string {
	if os.Getenv("NO_COLOR") != "" {
		return glyph
	}
	return lipgloss.NewStyle().Foreground(color).Render(glyph)
}

type rgb struct{ r, g, b int }

type tone struct {
	h, s, v float64
}

func (c rgb) hex() string {
	return fmt.Sprintf("#%02x%02x%02x", c.r, c.g, c.b)
}

func (c tone) brightness(amount float64) tone {
	c.v = max(0, min(1, c.v+amount))
	return c
}

func (c tone) saturation(amount float64) tone {
	c.s = max(0, min(1, c.s+amount))
	return c
}

func (c tone) rgb() rgb {
	h := math.Mod(c.h, 360)
	if h < 0 {
		h += 360
	}
	chroma := c.v * c.s
	x := chroma * (1 - math.Abs(math.Mod(h/60, 2)-1))
	var r, g, b float64
	switch {
	case h < 60:
		r, g = chroma, x
	case h < 120:
		r, g = x, chroma
	case h < 180:
		g, b = chroma, x
	case h < 240:
		g, b = x, chroma
	case h < 300:
		r, b = x, chroma
	default:
		r, b = chroma, x
	}
	m := c.v - chroma
	return rgb{int((r + m) * 255), int((g + m) * 255), int((b + m) * 255)}
}

func buildGradient(steps int, start, end tone) []lipgloss.Color {
	colors := make([]lipgloss.Color, steps)
	for i := range steps {
		position := float64(i) / float64(max(1, steps-1))
		current := tone{
			h: start.h + (end.h-start.h)*position,
			s: start.s + (end.s-start.s)*position,
			v: start.v + (end.v-start.v)*position,
		}
		colors[i] = lipgloss.Color(current.rgb().hex())
	}
	return colors
}

func adjust(color lipgloss.Color, saturation, brightness float64) lipgloss.Color {
	key := struct {
		Color                  lipgloss.Color
		Saturation, Brightness float64
	}{color, saturation, brightness}
	if cached, ok := adjustedColors.Load(key); ok {
		return cached.(lipgloss.Color)
	}
	value := string(color)
	if len(value) != 7 || value[0] != '#' {
		return color
	}
	r, errR := strconv.ParseInt(value[1:3], 16, 0)
	g, errG := strconv.ParseInt(value[3:5], 16, 0)
	b, errB := strconv.ParseInt(value[5:7], 16, 0)
	if errR != nil || errG != nil || errB != nil {
		return color
	}
	result := rgbTone(rgb{int(r), int(g), int(b)}).saturation(saturation).brightness(brightness).rgb()
	adjusted := lipgloss.Color(result.hex())
	adjustedColors.Store(key, adjusted)
	return adjusted
}

func rgbTone(c rgb) tone {
	r, g, b := float64(c.r)/255, float64(c.g)/255, float64(c.b)/255
	maximum := max(r, g, b)
	minimum := min(r, g, b)
	delta := maximum - minimum
	hue := 0.0
	if delta != 0 {
		switch maximum {
		case r:
			hue = 60 * math.Mod((g-b)/delta, 6)
		case g:
			hue = 60 * ((b-r)/delta + 2)
		case b:
			hue = 60 * ((r-g)/delta + 4)
		}
	}
	if hue < 0 {
		hue += 360
	}
	saturation := 0.0
	if maximum != 0 {
		saturation = delta / maximum
	}
	return tone{hue, saturation, maximum}
}
func Spark(ns []int) string {
	peak := 1
	for _, n := range ns {
		peak = max(peak, n)
	}
	var b strings.Builder
	glyphs := []rune("▁▂▃▄▅▆▇█")
	for _, n := range ns {
		b.WriteRune(glyphs[min(7, n*7/peak)])
	}
	return b.String()
}

// Overlay paints box over base at cell (x, y) without disturbing the surrounding layout.
func Overlay(base, box string, x, y int) string {
	lines := strings.Split(base, "\n")
	boxLines := strings.Split(box, "\n")
	boxWidth := lipgloss.Width(box)
	for i, boxLine := range boxLines {
		row := y + i
		if row < 0 || row >= len(lines) {
			continue
		}
		line := lines[row]
		width := ansi.StringWidth(line)
		left := ansi.Cut(line, 0, x)
		if pad := x - ansi.StringWidth(left); pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		right := ""
		if width > x+boxWidth {
			right = ansi.Cut(line, x+boxWidth, width)
		}
		lines[row] = left + "\x1b[0m" + lipgloss.NewStyle().Width(boxWidth).Render(boxLine) + "\x1b[0m" + right
	}
	return strings.Join(lines, "\n")
}

func Tooltip(text string) string {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(Accent).Background(Surface).Foreground(Text).Padding(0, 1).Render(text)
}

// Blend mixes two hex colors, with amount 0 giving a and 1 giving b.
func Blend(a, b lipgloss.Color, amount float64) lipgloss.Color {
	amount = max(0, min(1, amount))
	from, okA := parseHex(a)
	to, okB := parseHex(b)
	if !okA || !okB {
		return a
	}
	mix := func(x, y int) int { return int(float64(x) + (float64(y)-float64(x))*amount + .5) }
	return lipgloss.Color(rgb{mix(from.r, to.r), mix(from.g, to.g), mix(from.b, to.b)}.hex())
}

func parseHex(color lipgloss.Color) (rgb, bool) {
	value := string(color)
	if len(value) != 7 || value[0] != '#' {
		return rgb{}, false
	}
	r, errR := strconv.ParseInt(value[1:3], 16, 0)
	g, errG := strconv.ParseInt(value[3:5], 16, 0)
	b, errB := strconv.ParseInt(value[5:7], 16, 0)
	if errR != nil || errG != nil || errB != nil {
		return rgb{}, false
	}
	return rgb{int(r), int(g), int(b)}, true
}
