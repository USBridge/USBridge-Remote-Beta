package view

// connections_section_header.go -- the bar directly above the saved-
// connections list: title + category-count badges on the left, the
// Add/QR/paste-link actions on the right (moved here from the old floating
// FAB cluster and the mobile-only inline Add button in
// connection_manager_view.go), and a thin accent line along the bottom that
// stops short of the component's own right edge.

import (
	"fmt"
	"image/color"
	"strings"

	"usbridge-client/internal/gui/assets"
	"usbridge-client/internal/gui/design"
	"usbridge-client/internal/gui/i18n"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// connectionsHeaderSideMargin is the shared left/right margin for both the
// header's content row and its underline -- keeping them sharing one margin
// is what keeps the underline's left edge aligned with the row's, instead of
// running flush to the screen edge while the content doesn't.
const connectionsHeaderSideMargin float32 = 20

// connectionsHeaderUnderlineRightPullback is how much further short of the
// shared right margin the underline stops, on top of that margin -- the
// "doesn't quite reach the edge" look. Kept tiny: even 6 read as falling
// short of the buttons rather than a subtle pullback.
const connectionsHeaderUnderlineRightPullback float32 = 2

// alwaysShowConnectionsBadges is a temporary preview switch: normally a
// badge with a 0 count wouldn't render at all (nothing to say), but while
// we're still designing how they look, showing "0 Agent"/"0 KVM" is more
// useful than an empty header. Flip back to false once the look is settled.
const alwaysShowConnectionsBadges = true

// ConnectionsSummary is how many saved connections fall into each category
// shown as a count badge next to the section title.
type ConnectionsSummary struct {
	AgentCount int
	KVMCount   int
}

// ClassifyConnectionRemoteOS buckets a saved connection's RemoteOS value
// into the two categories the section header's badges count: a software
// Agent running on a regular desktop OS, or the USBridge-KVM hardware
// itself. Same distinction osIconResource's per-row icon choice already
// makes (RemoteOS "usbridge" vs "linux"/"windows"/"darwin"/"mac"), just
// aggregated into counts here instead of a per-row icon.
func ClassifyConnectionRemoteOS(remoteOS string) (isAgent bool, isKVM bool) {
	normalized := strings.ToLower(strings.TrimSpace(remoteOS))
	switch {
	case strings.Contains(normalized, "usbridge"):
		return false, true
	case strings.Contains(normalized, "linux"), strings.Contains(normalized, "windows"), strings.Contains(normalized, "darwin"), strings.Contains(normalized, "mac"):
		return true, false
	default:
		// Unclassified (empty/unknown RemoteOS, e.g. a connection never
		// successfully reached yet) -- counted in neither badge rather than
		// guessed at.
		return false, false
	}
}

// SummarizeConnections counts a batch of saved connections' RemoteOS values
// into the badges' two categories. Callers pass RemoteOS strings, not full
// connection records, so this package doesn't need to know the controller's
// SavedConnection type.
func SummarizeConnections(remoteOSValues []string) ConnectionsSummary {
	var summary ConnectionsSummary
	for _, remoteOS := range remoteOSValues {
		isAgent, isKVM := ClassifyConnectionRemoteOS(remoteOS)
		if isAgent {
			summary.AgentCount++
		}
		if isKVM {
			summary.KVMCount++
		}
	}
	return summary
}

// connectionsHeaderActions are the events the connections section header's
// action buttons can report.
type connectionsHeaderActions struct {
	OnAdd       func()
	OnQR        func()
	OnPasteLink func()
	// OnViewModeChange fires with "list" or "grid" when the Grid/List toggle
	// is tapped -- see ConnectionManagerUI.setViewMode.
	OnViewModeChange func(mode string)
	// OnSortToggle fires with "kvm", "agent", or "" (tapping the already-
	// active badge turns it back off) when a KVM/Agent count badge is
	// tapped -- see ConnectionManager.handleConnectionSortToggle.
	OnSortToggle func(kind string)
}

// connectionsHeaderButtons holds the header's own action button instances,
// so SetActionButtonsDisabled (connection_manager_view.go) can grey them out
// while a connection attempt is in flight -- the same reason
// ConnectionManagerUI already tracked its old topQRBtn/topAddBtn/etc.
type connectionsHeaderButtons struct {
	add *iconChromeButton
}

func (b *connectionsHeaderButtons) SetDisabled(disabled bool) {
	if b == nil {
		return
	}
	if b.add != nil {
		b.add.SetDisabled(disabled)
	}
}

// headerActionButtonHeight/IconButtonSize size the connections section
// header's Add button.
const headerAddButtonHeight float32 = 29

// connectionsHeaderButtonGap is the air between the view-mode toggle and the
// Add button -- wider than the tight spacing inside the toggle itself
// (connectionsViewModeToggleGap).
const connectionsHeaderButtonGap float32 = 10

// connectionsHeaderSubtitle sits directly under the section title. Plain
// English literal for now rather than routed through i18n -- the copy is
// still being sketched out, not ready to lock into translation files yet.
const connectionsHeaderSubtitle = "Your active remote desktop and hardware control sessions."

// newConnectionsHeader builds the bar shown above the saved-connections
// list (populated or empty): section title + subtitle + category-count
// badges on the left, view-mode toggle + Add/QR/paste-link actions on the
// right, thin accent line along the bottom. Returns the button instances
// too, for SetActionButtonsDisabled. activeSort ("", "kvm", or "agent")
// says which count badge (if any) is currently pressed/highlighted -- see
// ConnectionManagerUI.activeSort.
func newConnectionsHeader(summary ConnectionsSummary, actions connectionsHeaderActions, viewMode string, activeSort string) (fyne.CanvasObject, *connectionsHeaderButtons) {
	title := NewBrandText(strings.TrimSpace(i18n.Current.SavedConnections), 18, design.ColorConnectionsSectionTitle, true)

	// toggleSort returns kind's own tap handler: tapping the already-active
	// badge turns sorting back off (empty kind) instead of re-applying it.
	toggleSort := func(kind string) func() {
		return func() {
			next := kind
			if activeSort == kind {
				next = ""
			}
			if actions.OnSortToggle != nil {
				actions.OnSortToggle(next)
			}
		}
	}

	// container.NewHBox stretches every child to the row's full height (the
	// tallest child's), which would otherwise blow the badges up to the
	// title's height -- wrap each mismatched-height item in NewCenter so it
	// keeps its own natural size instead.
	titleGap := canvas.NewRectangle(color.Transparent)
	titleGap.SetMinSize(fyne.NewSize(10, 1))
	titleItems := []fyne.CanvasObject{container.NewCenter(title), titleGap}
	if summary.AgentCount > 0 || alwaysShowConnectionsBadges {
		titleItems = append(titleItems, container.NewCenter(newConnectionSortBadge(
			fmt.Sprintf("%d Agent", summary.AgentCount), design.ColorConnectionBadgeText,
			activeSort == "agent", toggleSort("agent"))))
	}
	if summary.KVMCount > 0 || alwaysShowConnectionsBadges {
		titleItems = append(titleItems, container.NewCenter(newConnectionSortBadge(
			fmt.Sprintf("%d KVM", summary.KVMCount), design.ColorConnectionAddFill,
			activeSort == "kvm", toggleSort("kvm"))))
	}
	titleRow := container.NewHBox(titleItems...)

	subtitle := canvas.NewText(connectionsHeaderSubtitle, design.ColorConnectionsSectionSubtitle)
	subtitle.TextSize = 10

	left := container.NewVBox(titleRow, subtitle)

	viewToggle := newConnectionsViewModeToggle(viewMode, actions.OnViewModeChange)

	plusSVG := `<svg viewBox="0 0 24 24" fill="#4c6803"><path d="M19 13h-6v6h-2v-6H5v-2h6V5h2v6h6v2z"/></svg>`
	plusIcon := fyne.NewStaticResource("add.svg", []byte(plusSVG))

	addBtn := newIconChromeButton(iconChromeButtonSpec{
		NormalFill:   design.ColorConnectionAddFill,
		HoverFill:    design.ColorConnectionAddFillHover,
		DisabledFill: connectionActionBlockedFill,
		Stroke:       color.Transparent,
		LabelColor:   color.NRGBA{R: 0x4c, G: 0x68, B: 0x03, A: 0xff},
		LabelSize:    10,
		LabelBold:    true,
		NormalIcon:   plusIcon,
		HoverIcon:    plusIcon,
		IconSize:     fyne.NewSize(18, 18),
		ButtonSize:   fyne.NewSize(0, headerAddButtonHeight),
		OnTapped:     actions.OnAdd,
	})
	addBtn.SetText(strings.TrimSpace(i18n.Current.AddConnectionTitle))

	// QR and paste-link buttons used to live here too. Dropped: QR is still
	// reachable from inside the Add Connection dialog itself
	// (connection_manager_dialogs.go's onQR), but paste-link
	// (actions.OnPasteLink, ultimately ConnectionManager.handlePasteLink)
	// has no other trigger left anywhere in the UI after this -- flagging
	// that rather than quietly stranding it.

	// NewCenter around viewToggle specifically: it's deliberately shorter
	// than addBtn, and this row would otherwise stretch it up to match it
	// (the same reason the row below wraps left/right in Center).
	// DeviceRowControlsLayout instead of a bare HBox so the gap here is an
	// explicit value (connectionsHeaderButtonGap) rather than the theme's
	// default spacing.
	right := container.New(&DeviceRowControlsLayout{Gap: connectionsHeaderButtonGap}, container.NewCenter(viewToggle), addBtn)

	// NewCenter, not the bare HBox, on both sides here: left is now two
	// lines tall (title row + subtitle), and without this the outer HBox
	// below would stretch right's buttons to match that height instead of
	// keeping their own fixed sizes.
	row := container.NewHBox(container.NewCenter(left), layout.NewSpacer(), container.NewCenter(right))

	// Bottom accent line: left edge shares the same margin as the content
	// row (see the outer NewInset below), right edge pulled back further
	// still by connectionsHeaderUnderlineRightPullback -- both measured from
	// that shared margin, not from the screen edge.
	accentLine := canvas.NewRectangle(design.ColorConnectionsSectionUnderline)
	accentLine.SetMinSize(fyne.NewSize(1, 0.5))
	underlineRightGap := canvas.NewRectangle(color.Transparent)
	underlineRightGap.SetMinSize(fyne.NewSize(connectionsHeaderUnderlineRightPullback, 1))
	underline := container.NewBorder(nil, nil, nil, underlineRightGap, accentLine)

	content := container.NewBorder(NewInset(row, 0, 0, 4, 8), underline, nil, nil)

	return NewInset(content, connectionsHeaderSideMargin, connectionsHeaderSideMargin, 8, 0), &connectionsHeaderButtons{add: addBtn}
}

// connectionSortBadge is one small pill badge (e.g. "2 Agent") that also
// works as this category's sort toggle -- deliberately hint-sized (short,
// small text), not button-sized. Inactive keeps the header's original
// look: dark fill, thin border, text in the category's own accent color
// (see design.ColorConnectionBadge{Border,Fill,Text}'s doc comment).
// active swaps that for a solid fill in the category's accent color with
// dark text -- the same "lit up" treatment Save/Apply/Connect already use
// for their own pressed states.
type connectionSortBadge struct {
	widget.BaseWidget

	labelText string
	accent    color.Color
	active    bool
	hovered   bool
	onTapped  func()

	bg    *canvas.Rectangle
	label *canvas.Text
}

func newConnectionSortBadge(text string, accent color.Color, active bool, onTapped func()) *connectionSortBadge {
	b := &connectionSortBadge{labelText: text, accent: accent, active: active, onTapped: onTapped}
	b.ExtendBaseWidget(b)
	return b
}

func (b *connectionSortBadge) CreateRenderer() fyne.WidgetRenderer {
	b.bg = canvas.NewRectangle(design.ColorConnectionBadgeFill)
	// A radius bigger than the badge could ever be tall just guarantees a
	// full pill (fully rounded ends) regardless of exactly how the text
	// measures, rather than tuning this to match one specific height.
	b.bg.CornerRadius = 20
	b.bg.StrokeColor = design.ColorConnectionBadgeBorder
	b.bg.StrokeWidth = 1

	b.label = canvas.NewText(b.labelText, b.accent)
	b.label.TextSize = 8
	b.label.TextStyle = fyne.TextStyle{Bold: true}

	b.refreshVisuals()
	content := NewInset(container.NewCenter(b.label), 8, 8, 1, 1)
	return widget.NewSimpleRenderer(container.NewStack(b.bg, content))
}

func (b *connectionSortBadge) refreshVisuals() {
	if b.bg == nil {
		return
	}
	if b.active {
		b.bg.FillColor = b.accent
		b.bg.StrokeColor = color.Transparent
		b.label.Color = design.ColorGray950
	} else {
		fill := color.Color(design.ColorConnectionBadgeFill)
		if b.hovered {
			fill = design.ColorSurfaceLight
		}
		b.bg.FillColor = fill
		b.bg.StrokeColor = design.ColorConnectionBadgeBorder
		b.label.Color = b.accent
	}
	b.bg.Refresh()
	b.label.Refresh()
}

func (b *connectionSortBadge) Tapped(*fyne.PointEvent) {
	if b.onTapped != nil {
		b.onTapped()
	}
}

func (b *connectionSortBadge) TappedSecondary(*fyne.PointEvent) {}

func (b *connectionSortBadge) Cursor() desktop.Cursor {
	return desktop.PointerCursor
}

func (b *connectionSortBadge) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.refreshVisuals()
}

func (b *connectionSortBadge) MouseMoved(*desktop.MouseEvent) {}

func (b *connectionSortBadge) MouseOut() {
	b.hovered = false
	b.refreshVisuals()
}

// connectionsViewModeToggle is the small "Grid | List" pair before the Add
// button -- a segmented pair of square buttons, not the pill-shaped
// Tailscale-style toggle, and shorter than the Add button next to it.
// Sketch-only for now: switches its own highlighted side on tap, doesn't
// yet drive an actual grid/list layout for the connections list.
const connectionsViewModeToggleSize float32 = 16

// connectionsViewModeButtonRadius is deliberately smaller than
// design.RadiusMD: at this button's small size, the default radius reads as
// a full pill rather than the "squarer" look meant to echo the track's own
// (proportionally gentler) rounding.
const connectionsViewModeButtonRadius float32 = 3

// connectionsViewModeToggleGap is the tight spacing *inside* the toggle:
// between its icon and label, and between the Grid and List buttons
// themselves. Deliberately much smaller than connectionsHeaderButtonGap
// (the air between this whole toggle and Add/QR/paste-link).
const connectionsViewModeToggleGap float32 = 3

func newConnectionsViewModeToggle(initialMode string, onChange func(mode string)) fyne.CanvasObject {
	var gridBtn, listBtn *iconChromeButton

	// List is the real current layout (the connections list has no grid
	// mode yet), so it starts as the highlighted side.
	setActive := func(listActive bool) {
		gridIcon, listIcon := assets.GridViewIconMuted, assets.ListViewIconAccent
		gridColor, listColor := design.ColorConnectionsSectionMutedText, design.ColorConnectionsSectionIcon
		gridFill, listFill := design.ColorGray900, design.ColorSurfaceLight
		if !listActive {
			gridIcon, listIcon = assets.GridViewIconAccent, assets.ListViewIconMuted
			gridColor, listColor = design.ColorConnectionsSectionIcon, design.ColorConnectionsSectionMutedText
			gridFill, listFill = design.ColorSurfaceLight, design.ColorGray900
		}
		gridBtn.spec.NormalFill = gridFill
		gridBtn.SetLabelColor(gridColor)
		gridBtn.SetIcons(gridIcon, gridIcon, gridIcon) // also triggers the redraw for both changes above
		listBtn.spec.NormalFill = listFill
		listBtn.SetLabelColor(listColor)
		listBtn.SetIcons(listIcon, listIcon, listIcon)
	}

	gridBtn = newIconChromeButton(iconChromeButtonSpec{
		NormalFill:   design.ColorGray900,
		HoverFill:    design.ColorBorder,
		Stroke:       color.Transparent,
		NormalIcon:   assets.GridViewIconMuted,
		IconSize:     fyne.NewSize(9, 9),
		ButtonSize:   fyne.NewSize(0, connectionsViewModeToggleSize),
		CornerRadius: connectionsViewModeButtonRadius,
	})
	gridBtn.SetText("Grid")
	listBtn = newIconChromeButton(iconChromeButtonSpec{
		NormalFill:   design.ColorSurfaceLight,
		HoverFill:    design.ColorBorder,
		Stroke:       color.Transparent,
		NormalIcon:   assets.ListViewIconAccent,
		IconSize:     fyne.NewSize(9, 9),
		ButtonSize:   fyne.NewSize(0, connectionsViewModeToggleSize),
		CornerRadius: connectionsViewModeButtonRadius,
	})
	listBtn.SetText("List")
	gridBtn.SetOnTapped(func() {
		setActive(false)
		if onChange != nil {
			onChange("grid")
		}
	})
	listBtn.SetOnTapped(func() {
		setActive(true)
		if onChange != nil {
			onChange("list")
		}
	})
	setActive(initialMode != "grid") // anything but "grid" starts List-active

	track := canvas.NewRectangle(design.ColorGray900)
	track.CornerRadius = design.RadiusMD
	track.StrokeColor = design.ColorHeaderAccentLine
	track.StrokeWidth = 1

	buttons := container.New(&DeviceRowControlsLayout{Gap: connectionsViewModeToggleGap}, gridBtn, listBtn)
	return container.NewStack(track, NewInset(buttons, 1, 1, 1, 1))
}
