package gui

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coyim/coyim/i18n"
	"github.com/coyim/coyim/otr_client"
	rosters "github.com/coyim/coyim/roster"
	"github.com/coyim/coyim/session/access"
	"github.com/coyim/coyim/ui"
	"github.com/coyim/coyim/xmpp/jid"
	"github.com/coyim/gotk3adapter/gdki"
	"github.com/coyim/gotk3adapter/glibi"
	"github.com/coyim/gotk3adapter/gtki"
)

var (
	enableWindow  glibi.Signal
	disableWindow glibi.Signal
)

type conversationView interface {
	appendMessage(sent sentMessage)
	appendPendingDelayed()
	appendStatus(from string, timestamp time.Time, show, showStatus string, gone bool)
	delayedMessageSent(int)
	displayNotification(notification string)
	displayNotificationVerifiedOrNot(u *gtkUI, notificationV, notificationNV string)
	isFileTransferNotifCanceled() bool
	isVisible() bool
	haveShownPrivateEndedNotification()
	haveShownPrivateNotification()
	newFileTransfer(fileName string, dir, send, receive bool) *fileNotification
	removeIdentityVerificationWarning()
	setEnabled(enabled bool)
	showIdentityVerificationWarning(*gtkUI)
	showSMPRequestForSecret(string)
	showSMPSuccess()
	showSMPFailure()
	updateFileTransfer(file *fileNotification)
	updateFileTransferNotificationCounts()
	updateSecurityWarning()

	show(userInitiated bool)
	destroy()
}

func (conv *conversationPane) showSMPRequestForSecret(question string) {
	conv.verifier.removeInProgressDialogs()
	conv.verifier.displayRequestForSecret(question)
}

func (conv *conversationPane) showSMPSuccess() {
	conv.verifier.removeInProgressDialogs()
	conv.verifier.displayVerificationSuccess()
}

func (conv *conversationPane) showSMPFailure() {
	conv.verifier.removeInProgressDialogs()
	conv.verifier.displayVerificationFailure()
}

type conversationWindow struct {
	*conversationPane
	win       gtki.Window
	parentWin gtki.Window
}

type securityWarningNotification struct {
	area        gtki.Box
	image       gtki.Image
	label       gtki.Label
	labelButton gtki.Label
}

type conversationPane struct {
	to                   jid.Any
	account              *account
	widget               gtki.Box
	menubar              gtki.MenuBar
	encryptedLabel       gtki.Label
	entry                gtki.TextView
	entryScroll          gtki.ScrolledWindow
	history              gtki.TextView
	pending              gtki.TextView
	scrollHistory        gtki.ScrolledWindow
	scrollPending        gtki.ScrolledWindow
	notificationArea     gtki.Box
	fileTransferNotif    *fileTransferNotification
	securityWarningNotif *securityWarningNotification
	verificationWarning  gtki.InfoBar
	colorSet             colorSet
	// The window to set dialogs transient for
	transientParent gtki.Window
	sync.Mutex
	marks                []*timedMark
	hidden               bool
	shiftEnterSends      bool
	afterNewMessage      func()
	currentPeer          func() (*rosters.Peer, bool)
	delayed              map[int]sentMessage
	pendingDelayed       []int
	pendingDelayedLock   sync.Mutex
	shownPrivate         bool
	isNewFingerprint     bool
	hasSetNewFingerprint bool
	verifier             *verifier
}

type tags struct {
	table gtki.TextTagTable
}

func (u *gtkUI) getTags() *tags {
	if u.tags == nil {
		u.tags = u.newTags()
	}
	return u.tags
}

func (u *gtkUI) newTags() *tags {
	cs := u.currentColorSet()
	t := new(tags)

	t.table, _ = g.gtk.TextTagTableNew()

	outgoingUser, _ := g.gtk.TextTagNew("outgoingUser")
	outgoingUser.SetProperty("foreground", cs.conversationOutgoingUserForeground)

	incomingUser, _ := g.gtk.TextTagNew("incomingUser")
	incomingUser.SetProperty("foreground", cs.conversationIncomingUserForeground)

	outgoingText, _ := g.gtk.TextTagNew("outgoingText")
	outgoingText.SetProperty("foreground", cs.conversationOutgoingTextForeground)

	incomingText, _ := g.gtk.TextTagNew("incomingText")
	incomingText.SetProperty("foreground", cs.conversationIncomingTextForeground)

	statusText, _ := g.gtk.TextTagNew("statusText")
	statusText.SetProperty("foreground", cs.conversationStatusTextForeground)

	timestampText, _ := g.gtk.TextTagNew("timestamp")
	timestampText.SetProperty("foreground", cs.timestampForeground)

	outgoingDelayedUser, _ := g.gtk.TextTagNew("outgoingDelayedUser")
	outgoingDelayedUser.SetProperty("foreground", cs.conversationOutgoingDelayedUserForeground)

	outgoingDelayedText, _ := g.gtk.TextTagNew("outgoingDelayedText")
	outgoingDelayedText.SetProperty("foreground", cs.conversationOutgoingDelayedTextForeground)

	t.table.Add(outgoingUser)
	t.table.Add(incomingUser)
	t.table.Add(outgoingText)
	t.table.Add(incomingText)
	t.table.Add(statusText)
	t.table.Add(outgoingDelayedUser)
	t.table.Add(outgoingDelayedText)
	t.table.Add(timestampText)

	return t
}

func (t *tags) createTextBuffer() gtki.TextBuffer {
	buf, _ := g.gtk.TextBufferNew(t.table)
	return buf
}

func getTextBufferFrom(e gtki.TextView) gtki.TextBuffer {
	tb, _ := e.GetBuffer()
	return tb
}

func getTextFrom(e gtki.TextView) string {
	tb := getTextBufferFrom(e)
	return tb.GetText(tb.GetStartIter(), tb.GetEndIter(), false)
}

func insertEnter(e gtki.TextView) {
	tb := getTextBufferFrom(e)
	tb.InsertAtCursor("\n")
}

func clearIn(e gtki.TextView) {
	tb := getTextBufferFrom(e)
	tb.Delete(tb.GetStartIter(), tb.GetEndIter())
}

func (conv *conversationWindow) isVisible() bool {
	return conv.win.HasToplevelFocus()

}

func (conv *conversationPane) onSendMessageSignal() {
	conv.entry.SetEditable(false)
	text := getTextFrom(conv.entry)
	clearIn(conv.entry)
	conv.entry.SetEditable(true)
	if text != "" {
		sendError := conv.sendMessage(text)

		if sendError != nil {
			log.Printf(i18n.Local("Failed to generate OTR message: %s\n"), sendError.Error())
		}
	}
	conv.entry.GrabFocus()
}

func (conv *conversationPane) currentResource() jid.Resource {
	return jid.Resource(conv.mapCurrentPeer("", func(p *rosters.Peer) string {
		return string(p.ResourceToUse())
	}))
}

func (conv *conversationPane) onStartOtrSignal() {
	//TODO: enable/disable depending on the conversation's encryption state
	session := conv.account.session
	c, _ := session.ConversationManager().EnsureConversationWith(conv.to.MaybeWithResource(conv.currentResource()))
	err := c.StartEncryptedChat()
	if err != nil {
		log.Printf(i18n.Local("Failed to start encrypted chat: %s\n"), err.Error())
	} else {
		conv.displayNotification(i18n.Local("Attempting to start a private conversation..."))
	}
}

func (conv *conversationPane) onEndOtrSignal() {
	//TODO: enable/disable depending on the conversation's encryption state
	session := conv.account.session
	err := session.ManuallyEndEncryptedChat(conv.to.NoResource(), conv.currentResource())

	if err != nil {
		log.Printf(i18n.Local("Failed to terminate the encrypted chat: %s\n"), err.Error())
	} else {

		conv.removeIdentityVerificationWarning()
		conv.displayNotification(i18n.Local("Private conversation has ended."))
		conv.updateSecurityWarning()
		conv.haveShownPrivateEndedNotification()
	}
}

func (conv *conversationPane) onVerifyFpSignal() {
	switch verifyFingerprintDialog(conv.account, conv.to.NoResource(), conv.currentResource(), conv.transientParent) {
	case gtki.RESPONSE_YES:
		conv.removeIdentityVerificationWarning()
	}
}

func (conv *conversationPane) onConnect() {
	conv.entry.SetEditable(true)
	conv.entry.SetSensitive(true)
}

func (conv *conversationPane) onDisconnect() {
	conv.entry.SetEditable(false)
	conv.entry.SetSensitive(false)
}

func countVisibleLines(v gtki.TextView) uint {
	lines := uint(1)
	iter := getTextBufferFrom(v).GetStartIter()
	for v.ForwardDisplayLine(iter) {
		lines++
	}

	return lines
}

func (conv *conversationPane) calculateHeight(lines uint) uint {
	return lines * 2 * getFontSizeFrom(conv.entry)
}

func (conv *conversationPane) doPotentialEntryResize() {
	lines := countVisibleLines(conv.entry)
	scroll := true
	if lines > 3 {
		scroll = false
		lines = 3
	}
	conv.entryScroll.SetProperty("height-request", conv.calculateHeight(lines))
	if scroll {
		scrollToTop(conv.entryScroll)
	}
}

func createConversationPane(account *account, uid jid.Any, ui *gtkUI, transientParent gtki.Window) *conversationPane {
	builder := newBuilder("ConversationPane")

	cp := &conversationPane{
		to:                   uid,
		account:              account,
		fileTransferNotif:    builder.fileTransferNotifInit(),
		securityWarningNotif: builder.securityWarningNotifInit(),
		transientParent:      transientParent,
		shiftEnterSends:      ui.settings.GetShiftEnterForSend(),
		afterNewMessage:      func() {},
		delayed:              make(map[int]sentMessage),
		currentPeer: func() (*rosters.Peer, bool) {
			return ui.getPeer(account, uid.NoResource())
		},
		colorSet: ui.currentColorSet(),
	}

	builder.getItems(
		"box", &cp.widget,
		"menuTag", &cp.encryptedLabel,
		"history", &cp.history,
		"pending", &cp.pending,
		"historyScroll", &cp.scrollHistory,
		"pendingScroll", &cp.scrollPending,
		"message", &cp.entry,
		"notification-area", &cp.notificationArea,
		"menubar", &cp.menubar,
		"messageScroll", &cp.entryScroll,
	)

	builder.ConnectSignals(map[string]interface{}{
		"on_start_otr_signal":      cp.onStartOtrSignal,
		"on_end_otr_signal":        cp.onEndOtrSignal,
		"on_verify_fp_signal":      cp.onVerifyFpSignal,
		"on_connect":               cp.onConnect,
		"on_disconnect":            cp.onDisconnect,
		"on_destroy_file_transfer": cp.onDestroyFileTransferNotif,
		// TODO: this stays clicked longer than it should
		"on_send_file_to_contact": func() {
			if peer, ok := ui.getPeer(account, uid.NoResource()); ok {
				// TODO: It's a real problem to start file transfer if we don't have a resource, so we should ensure that here
				// (Because disco#info will not actually return results from the CLIENT unless a resource is prefixed...

				doInUIThread(func() { account.sendFileTo(uid.WithResource(peer.MustHaveResource()), ui) })
			}
		},
	})

	transientParent.AddMnemonic(uint(115), cp.encryptedLabel)
	transientParent.SetMnemonicModifier(gdki.GDK_CONTROL_MASK)

	cp.entryScroll.SetProperty("height-request", cp.calculateHeight(1))

	prov := providerWithCSS("scrolledwindow { border-top: 2px solid #d3d3d3; } ")
	updateWithStyle(cp.entryScroll, prov)

	cp.history.SetBuffer(ui.getTags().createTextBuffer())
	cp.history.Connect("size-allocate", func() {
		scrollToBottom(cp.scrollHistory)
	})

	cp.pending.SetBuffer(ui.getTags().createTextBuffer())

	cp.entry.Connect("key-release-event", cp.doPotentialEntryResize)

	ui.displaySettings.control(cp.history)
	ui.displaySettings.shadeBackground(cp.pending)
	ui.displaySettings.control(cp.entry)
	ui.keyboardSettings.control(cp.entry)
	ui.keyboardSettings.update()

	cp.verifier = newVerifier(ui, cp)

	return cp
}

func (b *builder) securityWarningNotifInit() *securityWarningNotification {
	securityWarningNotif := &securityWarningNotification{}

	b.getItems(
		"security-warning", &securityWarningNotif.area,
		"image-security-warning", &securityWarningNotif.image,
		"label-security-warning", &securityWarningNotif.label,
		"button-label-security-warning", &securityWarningNotif.labelButton,
	)

	return securityWarningNotif
}

func (conv *conversationPane) connectEnterHandler(target gtki.Widget) {
	if target == nil {
		target = conv.entry
	}

	target.Connect("key-press-event", func(_ gtki.Widget, ev gdki.Event) bool {
		evk := g.gdk.EventKeyFrom(ev)
		ret := false

		if conv.account.isInsertEnter(evk, conv.shiftEnterSends) {
			insertEnter(conv.entry)
			ret = true
		} else if conv.account.isSend(evk, conv.shiftEnterSends) {
			conv.onSendMessageSignal()
			ret = true
		}

		return ret
	})
}

func isShiftEnter(evk gdki.EventKey) bool {
	return hasShift(evk) && hasEnter(evk)
}

func isNormalEnter(evk gdki.EventKey) bool {
	return !hasControlingModifier(evk) && hasEnter(evk)
}

func (a *account) isInsertEnter(evk gdki.EventKey, shiftEnterSends bool) bool {
	if shiftEnterSends {
		return isNormalEnter(evk)
	}
	return isShiftEnter(evk)
}

func (a *account) isSend(evk gdki.EventKey, shiftEnterSends bool) bool {
	if !shiftEnterSends {
		return isNormalEnter(evk)
	}
	return isShiftEnter(evk)
}

func newConversationWindow(account *account, uid jid.Any, ui *gtkUI, existing *conversationPane) *conversationWindow {
	builder := newBuilder("Conversation")
	win := builder.getObj("conversation").(gtki.Window)

	peer, ok := ui.accountManager.contacts[account].Get(uid.NoResource())
	otherName := uid.String()
	if ok {
		otherName = peer.NameForPresentation()
	}

	// TODO: Can we put the security rating here, maybe?
	title := fmt.Sprintf("%s <-> %s", account.session.DisplayName(), otherName)
	win.SetTitle(title)

	winBox := builder.getObj("box").(gtki.Box)

	cp := createConversationPane(account, uid, ui, win)
	if existing != nil {
		b, _ := existing.history.GetBuffer()
		cp.history.SetBuffer(b)
	}

	cp.menubar.Show()
	winBox.PackStart(cp.widget, true, true, 0)

	conv := &conversationWindow{
		conversationPane: cp,
		win:              win,
	}

	cp.connectEnterHandler(conv.win)
	cp.afterNewMessage = conv.potentiallySetUrgent

	// Unlike the GTK version, this is not supposed to be used as a callback but
	// it attaches the callback to the widget
	conv.win.HideOnDelete()

	inEventHandler := false
	conv.win.Connect("set-focus", func() {
		if !inEventHandler {
			inEventHandler = true
			conv.entry.GrabFocus()
			inEventHandler = false
		}
	})

	conv.win.Connect("focus-in-event", func() {
		conv.unsetUrgent()
	})

	conv.win.Connect("notify::is-active", func() {
		if conv.win.IsActive() {
			inEventHandler = true
			conv.entry.GrabFocus()
			inEventHandler = false
		}
	})

	conv.win.Connect("hide", func() {
		conv.onHide()
	})

	conv.win.Connect("show", func() {
		conv.onShow()
	})

	ui.connectShortcutsChildWindow(conv.win)
	ui.connectShortcutsConversationWindow(conv)
	conv.parentWin = ui.window

	return conv
}

func (conv *conversationWindow) destroy() {
	conv.win.Destroy()
}

func (conv *conversationWindow) tryEnsureCorrectWorkspace() {
	if !g.gdk.WorkspaceControlSupported() {
		return
	}

	wi, _ := conv.parentWin.GetWindow()
	parentPlace := wi.GetDesktop()
	cwi, _ := conv.win.GetWindow()
	cwi.MoveToDesktop(parentPlace)
}

func (conv *conversationPane) getConversation() (otr_client.Conversation, bool) {
	return conv.account.session.ConversationManager().GetConversationWith(conv.to.MaybeWithResource(conv.currentResource()))
}

func (conv *conversationPane) mapCurrentPeer(def string, f func(*rosters.Peer) string) string {
	if p, ok := conv.currentPeer(); ok {
		return f(p)
	}
	return def
}

func (conv *conversationPane) isVerified(u *gtkUI) bool {
	conversation, exists := conv.getConversation()
	if !exists {
		log.Println("Conversation does not exist - this shouldn't happen")
		return false
	}

	fingerprint := conversation.TheirFingerprint()
	conf := conv.account.session.GetConfig()

	p, hasPeer := conf.GetPeer(conv.to.NoResource().String())
	isNew := false

	if hasPeer {
		_, isNew = p.EnsureHasFingerprint(fingerprint)

		err := u.saveConfigInternal()
		if err != nil {
			log.Println("Failed to save config:", err)
		}
	} else {
		p = conf.EnsurePeer(conv.to.NoResource().String())
		p.EnsureHasFingerprint(fingerprint)

		err := u.saveConfigInternal()
		if err != nil {
			log.Println("Failed to save config:", err)
		}
	}

	if !conv.hasSetNewFingerprint {
		conv.isNewFingerprint = isNew
		conv.hasSetNewFingerprint = true
	}

	return hasPeer && p.HasTrustedFingerprint(fingerprint)
}

func (conv *conversationPane) showIdentityVerificationWarning(u *gtkUI) {
	conv.Lock()
	defer conv.Unlock()

	conv.verifier.showUnverifiedWarning()
}

func (conv *conversationPane) removeIdentityVerificationWarning() {
	conv.Lock()
	defer conv.Unlock()

	conv.verifier.removeInProgressDialogs()
	conv.verifier.hideUnverifiedWarning()
}

func (conv *conversationPane) updateSecurityWarning() {
	conversation, ok := conv.getConversation()

	prov := providerWithCSS("box { background-color: #fff3f3;  color: #000000; border: 3px; }")
	updateWithStyle(conv.securityWarningNotif.area, prov)

	conv.securityWarningNotif.label.SetLabel("You are talking over an \nunprotected chat")
	setImageFromFile(conv.securityWarningNotif.image, "secure.svg")
	conv.securityWarningNotif.area.SetVisible(!ok || !conversation.IsEncrypted())
}

func updateEntryBackground(entry gtki.TextView, color string) {
	css := fmt.Sprintf("text { background-color: %s; }", color)
	prov := providerWithCSS(css)
	updateWithStyle(entry, prov)
}

func setCursor(entry gtki.TextView, hasCursor bool) {
	entry.SetCursorVisible(hasCursor)
	entry.SetEditable(hasCursor)
	entry.SetCanFocus(hasCursor)
}

func (conv *conversationWindow) show(userInitiated bool) {
	conv.updateSecurityWarning()
	if userInitiated {
		conv.win.Present() // Raises the window
	} else {
		conv.win.Show()
	}
	conv.tryEnsureCorrectWorkspace()
}

const mePrefix = "/me "

type sentMessage struct {
	message         string
	strippedMessage []byte
	from            string
	to              jid.WithoutResource
	resource        jid.Resource
	timestamp       time.Time
	queuedTimestamp time.Time
	isEncrypted     bool
	isDelayed       bool
	isOutgoing      bool
	isResent        bool
	trace           int
	coordinates     bufferSlice
}

func (sent *sentMessage) Tagged() ([]*taggableText, bool) {
	msgTxt := string(sent.strippedMessage)
	msgHasMePrefix := strings.HasPrefix(strings.TrimSpace(msgTxt), mePrefix)
	attention := !sent.isDelayed && !msgHasMePrefix
	userTag := is(sent.isOutgoing, "outgoingUser", "incomingUser")
	userTag = is(sent.isDelayed, "outgoingDelayedUser", userTag)
	textTag := is(sent.isOutgoing, "outgoingText", "incomingText")
	textTag = is(sent.isDelayed, "outgoingDelayedText", textTag)

	if sent.isDelayed {
		return []*taggableText{
			&taggableText{userTag, sent.from},
			&taggableText{text: ":  "},
			&taggableText{textTag, msgTxt},
		}, attention
	}

	if msgHasMePrefix {
		msgTxt = strings.TrimPrefix(strings.TrimSpace(msgTxt), mePrefix)
		return []*taggableText{
			&taggableText{userTag, sent.from + " " + msgTxt},
		}, attention
	}

	return []*taggableText{
		&taggableText{userTag, sent.from},
		&taggableText{text: ":  "},
		&taggableText{textTag, msgTxt},
	}, attention
}

func (conv *conversationPane) storeDelayedMessage(trace int, message sentMessage) {
	conv.pendingDelayedLock.Lock()
	defer conv.pendingDelayedLock.Unlock()

	conv.delayed[trace] = message
}

func (conv *conversationPane) haveShownPrivateNotification() {
	conv.shownPrivate = true
}

func (conv *conversationPane) haveShownPrivateEndedNotification() {
	conv.shownPrivate = false
}

func (conv *conversationPane) appendPendingDelayed() {
	conv.pendingDelayedLock.Lock()
	defer conv.pendingDelayedLock.Unlock()

	current := conv.pendingDelayed
	conv.pendingDelayed = nil

	for _, ctrace := range current {
		dm, ok := conv.delayed[ctrace]
		if ok {
			delete(conv.delayed, ctrace)
			conversation, _ := conv.account.session.ConversationManager().EnsureConversationWith(dm.to.MaybeWithResource(dm.resource))

			dm.isEncrypted = conversation.IsEncrypted()
			dm.queuedTimestamp = dm.timestamp
			dm.timestamp = time.Now()
			dm.isDelayed = false
			dm.isResent = true

			conv.appendMessage(dm)

			conv.markNow()
			doInUIThread(func() {
				conv.Lock()
				defer conv.Unlock()

				buff, _ := conv.pending.GetBuffer()
				buff.Delete(buff.GetIterAtMark(dm.coordinates.start), buff.GetIterAtMark(dm.coordinates.end))
			})
		}
	}

	conv.hideDelayedMessagesWindow()
}

func (conv *conversationPane) delayedMessageSent(trace int) {
	conv.pendingDelayedLock.Lock()
	conv.pendingDelayed = append(conv.pendingDelayed, trace)
	conv.pendingDelayedLock.Unlock()

	if conv.shownPrivate {
		conv.appendPendingDelayed()
	}

}

func (conv *conversationPane) sendMessage(message string) error {
	session := conv.account.session
	trace, delayed, err := session.EncryptAndSendTo(conv.to.NoResource(), conv.currentResource(), message)

	if err != nil {
		oerr, isoff := err.(*access.OfflineError)
		if !isoff {
			return err
		}

		conv.displayNotification(oerr.Error())
	} else {
		//TODO: review whether it should create a conversation
		//TODO: this should be whether the message was encrypted or not, rather than
		//whether the conversation is encrypted or not
		conversation, _ := session.ConversationManager().EnsureConversationWith(conv.to.MaybeWithResource(conv.currentResource()))

		sent := sentMessage{
			message:         message,
			strippedMessage: ui.StripSomeHTML([]byte(message)),
			from:            conv.account.session.DisplayName(),
			to:              conv.to.NoResource(),
			resource:        conv.currentResource(),
			timestamp:       time.Now(),
			isEncrypted:     conversation.IsEncrypted(),
			isDelayed:       delayed,
			isOutgoing:      true,
			trace:           trace,
		}

		if delayed {
			conv.showDelayedMessagesWindow()
		}
		conv.appendMessage(sent)
	}

	return nil
}

const timeDisplay = "15:04:05"

// Expects to be called from the GUI thread.
// Expects to be called when conv is already locked
func insertAtEnd(buff gtki.TextBuffer, text string) {
	buff.Insert(buff.GetEndIter(), text)
}

// Expects to be called from the GUI thread.
// Expects to be called when conv is already locked
func insertWithTag(buff gtki.TextBuffer, tagName, text string) {
	charCount := buff.GetCharCount()
	insertAtEnd(buff, text)
	oldEnd := buff.GetIterAtOffset(charCount)
	newEnd := buff.GetEndIter()
	buff.ApplyTagByName(tagName, oldEnd, newEnd)
}

func is(v bool, left, right string) string {
	if v {
		return left
	}
	return right
}

func showForDisplay(show string, gone bool) string {
	switch show {
	case "", "available", "online":
		if gone {
			return ""
		}
		return i18n.Local("Available")
	case "xa":
		return i18n.Local("Not Available")
	case "away":
		return i18n.Local("Away")
	case "dnd":
		return i18n.Local("Busy")
	case "chat":
		return i18n.Local("Free for Chat")
	case "invisible":
		return i18n.Local("Invisible")
	}
	return show
}

func onlineStatus(show, showStatus string) string {
	sshow := showForDisplay(show, false)
	if sshow != "" {
		return sshow + showStatusForDisplay(showStatus)
	}
	return ""
}

func showStatusForDisplay(showStatus string) string {
	if showStatus != "" {
		return " (" + showStatus + ")"
	}
	return ""
}

func extraOfflineStatus(show, showStatus string) string {
	sshow := showForDisplay(show, true)
	if sshow == "" {
		return showStatusForDisplay(showStatus)
	}

	if showStatus != "" {
		return " (" + sshow + ": " + showStatus + ")"
	}
	return " (" + sshow + ")"
}

func createStatusMessage(from, show, showStatus string, gone bool) string {
	tail := ""
	if gone {
		tail = i18n.Local("Offline") + extraOfflineStatus(show, showStatus)
	} else {
		tail = onlineStatus(show, showStatus)
	}

	if tail != "" {
		return from + i18n.Local(" is now ") + tail
	}
	return ""
}

func scrollToBottom(sw gtki.ScrolledWindow) {
	//TODO: Should only scroll if is at the end.
	adj := sw.GetVAdjustment()
	adj.SetValue(adj.GetUpper() - adj.GetPageSize())
}

func scrollToTop(sw gtki.ScrolledWindow) {
	adj := sw.GetVAdjustment()
	adj.SetValue(adj.GetLower())
}

type taggableText struct {
	tag  string
	text string
}

type bufferSlice struct {
	start, end gtki.TextMark
}

func (conv *conversationPane) appendSentMessage(sent sentMessage, attention bool, entries ...*taggableText) {
	conv.markNow()
	doInUIThread(func() {
		conv.Lock()
		defer conv.Unlock()

		var buff gtki.TextBuffer
		if sent.isDelayed {
			buff, _ = conv.pending.GetBuffer()
		} else {
			buff, _ = conv.history.GetBuffer()
		}

		start := buff.GetCharCount()
		if start != 0 {
			insertAtEnd(buff, "\n")
		}

		if sent.isResent {
			insertTimestamp(buff, sent.queuedTimestamp)
		}
		insertTimestamp(buff, sent.timestamp)

		for _, entry := range entries {
			insertEntry(buff, entry)
		}

		if sent.isDelayed {
			sent.coordinates.start, sent.coordinates.end = markInsertion(buff, sent.trace, start)
			conv.storeDelayedMessage(sent.trace, sent)
		}

		if attention {
			conv.afterNewMessage()
		}
	})
}

func markInsertion(buff gtki.TextBuffer, trace, startOffset int) (start, end gtki.TextMark) {
	insert := "insert" + strconv.Itoa(trace)
	selBound := "selection_bound" + strconv.Itoa(trace)
	start = buff.CreateMark(insert, buff.GetIterAtOffset(startOffset), false)
	end = buff.CreateMark(selBound, buff.GetEndIter(), false)
	return
}

func insertTimestamp(buff gtki.TextBuffer, timestamp time.Time) {
	insertWithTag(buff, "timestamp", "[")
	insertWithTag(buff, "timestamp", timestamp.Format(timeDisplay))
	insertWithTag(buff, "timestamp", "] ")
}

func insertEntry(buff gtki.TextBuffer, entry *taggableText) {
	if entry.tag != "" {
		insertWithTag(buff, entry.tag, entry.text)
	} else {
		insertAtEnd(buff, entry.text)
	}
}

func (conv *conversationPane) appendStatus(from string, timestamp time.Time, show, showStatus string, gone bool) {
	conv.appendSentMessage(sentMessage{timestamp: timestamp}, false, &taggableText{
		"statusText", createStatusMessage(from, show, showStatus, gone),
	})
}

func (conv *conversationPane) appendMessage(sent sentMessage) {
	entries, attention := sent.Tagged()
	conv.appendSentMessage(sent, attention, entries...)
}

func (conv *conversationPane) displayNotification(notification string) {
	conv.appendSentMessage(
		sentMessage{timestamp: time.Now()},
		false,
		&taggableText{"statusText", notification},
	)
}

func (conv *conversationPane) displayNotificationVerifiedOrNot(u *gtkUI, notificationV, notificationNV string) {
	isVerified := conv.isVerified(u)

	if isVerified {
		conv.displayNotification(notificationV)
	} else {
		conv.displayNotification(notificationNV)
	}

	if conv.isNewFingerprint {
		conv.displayNotification(i18n.Local("The peer is using a key we haven't seen before!"))
	}
}

func (conv *conversationWindow) setEnabled(enabled bool) {
	if enabled {
		conv.win.Emit("enable")
	} else {
		conv.win.Emit("disable")
	}
}

type timedMark struct {
	at     time.Time
	offset int
}

func (conv *conversationPane) markNow() {
	conv.Lock()
	defer conv.Unlock()

	buf, _ := conv.history.GetBuffer()
	offset := buf.GetCharCount()

	conv.marks = append(conv.marks, &timedMark{
		at:     time.Now(),
		offset: offset,
	})
}

const reapInterval = time.Duration(1) * time.Hour

func (conv *conversationPane) reapOlderThan(t time.Time) {
	conv.Lock()
	defer conv.Unlock()

	newMarks := []*timedMark{}
	var lastMark *timedMark
	isEnd := false

	for ix, m := range conv.marks {
		if t.Before(m.at) {
			newMarks = conv.marks[ix:]
			break
		}
		lastMark = m
		isEnd = len(conv.marks)-1 == ix
	}

	if lastMark != nil {
		off := lastMark.offset + 1
		buf, _ := conv.history.GetBuffer()
		sit := buf.GetStartIter()
		eit := buf.GetIterAtOffset(off)
		if isEnd {
			eit = buf.GetEndIter()
			newMarks = []*timedMark{}
		}

		buf.Delete(sit, eit)

		for _, nm := range newMarks {
			nm.offset = nm.offset - off
		}

		conv.marks = newMarks
	}
}

func (conv *conversationPane) onHide() {
	conv.reapOlderThan(time.Now().Add(-reapInterval))
	conv.hidden = true
}

func (conv *conversationPane) onShow() {
	if conv.hidden {
		conv.reapOlderThan(time.Now().Add(-reapInterval))
		conv.hidden = false
	}
}

func (conv *conversationPane) showDelayedMessagesWindow() {
	conv.scrollPending.SetVisible(true)
}

func (conv *conversationPane) hideDelayedMessagesWindow() {
	conv.scrollPending.SetVisible(false)
}

func (conv *conversationWindow) potentiallySetUrgent() {
	if !conv.win.HasToplevelFocus() {
		conv.win.SetUrgencyHint(true)
	}
}

func (conv *conversationWindow) unsetUrgent() {
	conv.win.SetUrgencyHint(false)
}
