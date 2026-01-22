package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	g "xabbo.b7c.io/goearth"
	"xabbo.b7c.io/goearth/shockwave/in"
	"xabbo.b7c.io/goearth/shockwave/out"
)

// Global variables for dice management, rolling state, mutex, and wait group
var (
	diceList         []*Dice
	mutedDuration    int
	isMuted          bool
	currentSum       int
	commandList      string
	isPokerRolling   bool
	isTriRolling     bool
	isBJRolling      bool
	is13Rolling      bool
	is13Hitting      bool
	isHitting        bool
	isClosing        bool
	ChatIsDisabled   bool
	mutex            sync.Mutex
	resultsWaitGroup sync.WaitGroup
	rollDelay        = 550 * time.Millisecond
		// Trade capture (one trade at a time)
	tradeOpen       bool
	tradePartner    string
	tradeItemClass  string
	tradeBetCount   int

	// Inventory cache built from STRIPINFO_2 after GETSTRIP new
	invCounts       = map[string]int{}
	invCollecting   bool
	invReady		bool
)
var (
	lastStripInfoAt time.Time
)

var tradeCanAutoAccept bool
var tradeNeeded int
var dealerAddedInTrade int
var autoTradeAccept = true
var tradeAcceptedByBot bool

// ---- Session (Step 1) ----
// One session at a time. Trades will hook into this later.
type Session struct {
	Active     bool
	PlayerName string

	// What item the player bet (e.g. "duck") and how many were bet for the CURRENT round
	ItemClass string
	BetCount  int

	// Bankroll tracked AFTER a win (e.g. bet 1 duck => Balance becomes 2)
	Balance int

	// State flags
	AwaitingGameChoice bool
	InGame             bool

	// Post-win options
	CanRisk    bool
	CanCashOut bool
}

var session Session

type App struct {
	ext    *g.Ext
	assets embed.FS
	log    []string
	logMu  sync.Mutex
	ctx    context.Context
}

type PokerDisplayConfig struct {
	FiveOfAKind  string `json:"five_of_a_kind"`
	FourOfAKind  string `json:"four_of_a_kind"`
	FullHouse    string `json:"full_house"`
	HighStraight string `json:"high_straight"`
	LowStraight  string `json:"low_straight"`
	ThreeOfAKind string `json:"three_of_a_kind"`
	TwoPair      string `json:"two_pair"`
	OnePair      string `json:"one_pair"`
	Nothing      string `json:"nothing"`
}

func NewApp(ext *g.Ext, assets embed.FS) *App {
	return &App{
		ext:    ext,
		assets: assets,
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.setupExt()
	go func() {
		a.runExt()
	}()
	// Warm inventory continuously so first trade has real counts
	go func() {
		time.Sleep(2 * time.Second)
		for {
			a.refreshInventoryAndWait(3 * time.Second)
			time.Sleep(8 * time.Second)
		}
	}()


}

func (a *App) LoadConfig() *PokerDisplayConfig {
	configFilePath := getConfigFilePath()

	file, err := os.Open(configFilePath)
	if err != nil {
		a.AddLogMsg("Config file not found, loading default values")
		return &PokerDisplayConfig{
			FiveOfAKind:  "Five of a kind: %s",
			FourOfAKind:  "Four of a kind: %s",
			FullHouse:    "Full House: %s",
			HighStraight: "High Str8",
			LowStraight:  "Low Str8",
			ThreeOfAKind: "Three of a kind: %s",
			TwoPair:      "Two Pair: %s",
			OnePair:      "One Pair: %s",
			Nothing:      "Nothing",
		}
	}
	defer file.Close()

	var config PokerDisplayConfig
	if err := json.NewDecoder(file).Decode(&config); err != nil {
		a.AddLogMsg("Error decoding config file: " + err.Error())
		return nil
	}

	// Config file loaded successfully
	return &config
}

func (a *App) SaveConfig(config *PokerDisplayConfig) {
	configFilePath := getConfigFilePath()

	file, err := os.Create(configFilePath)
	if err != nil {
		a.AddLogMsg("Error creating config file: " + err.Error())
		return
	}
	defer file.Close()

	if err := json.NewEncoder(file).Encode(config); err != nil {
		a.AddLogMsg("Error encoding config file: " + err.Error())
		return
	}

	a.AddLogMsg("Config file saved successfully")
}

func getConfigFilePath() string {
	configDir, _ := os.UserConfigDir()
	configPath := filepath.Join(configDir, "URTBOT")
	os.MkdirAll(configPath, 0700)
	return filepath.Join(configPath, "poker_display_config.json")
}

func (a *App) setupExt() {
	a.ext.Intercept(out.CHAT, out.SHOUT, out.WHISPER).With(a.onChatMessage)
	a.ext.Intercept(out.THROW_DICE).With(a.handleThrowDice)
	a.ext.Intercept(out.DICE_OFF).With(a.handleDiceOff)
	a.ext.Intercept(in.DICE_VALUE).With(a.handleDiceResult)
	a.ext.Intercept(out.CHAT).With(a.handleTalk)
	a.ext.Intercept(out.SHOUT).With(a.handleTalk)
	a.ext.InterceptAll(func(e *g.Intercept) {
	handleMutePacket(e)     // existing
	a.handleTradeAndInv(e)  // new Step 2
})
	// Register missing identifiers (Shockwave)
	a.ext.Initialized(func(args g.InitArgs) {
		// Outgoing[402] -> TRADE_CONFIRM_ACCEPT
		a.ext.Headers().Add("TRADE_CONFIRM_ACCEPT", g.Header{Dir: g.Out, Value: 402})
	})

}

func (a *App) runExt() {
	defer os.Exit(0)
	a.ext.Run()
}

func (a *App) ShowWindow() {
	runtime.WindowShow(a.ctx)
}

func startMuteTimer(duration int) {
	for duration > 0 {
		log.Printf("Remaining mute time: %d seconds", duration)
		time.Sleep(1 * time.Second) // Sleep for 1 second
		duration--
	}

	// Mute duration finished
	handleMuteEnd()
}

func handleMuteEnd() {
	isMuted = false
	log.Println("Mute finished, sending queued messages...")

	// ToDo:
	// // Send all queued messages
	// for _, message := range messageQueue {
	// 	sendMessageWithDelay(message)
	// }

	// // Clear the message queue
	// messageQueue = []string{}
}

// Mute detection logic (called within InterceptAll)
func handleMutePacket(e *g.Intercept) {
	// Check for the "first muted" packet with header 4069
	if e.Packet.Header.Value == 4069 {
		mutedDuration = e.Packet.ReadInt() // Read the mute duration in seconds
		log.Printf("You are muted for %d seconds.", mutedDuration)
		isMuted = true
		go startMuteTimer(mutedDuration) // Start the mute timer
	}

	// Check for the "trying to chat while muted" packet with header 3285
	if e.Packet.Header.Value == 3285 {
		remainingMuteDuration := e.Packet.ReadInt() // Read the remaining mute duration
		log.Printf("Mute still active, remaining time: %d seconds.", remainingMuteDuration)
	}
}

func (a *App) onChatMessage(e *g.Intercept) {
	msg := e.Packet.ReadString()

	// Process commands based on the message prefix and suffix
	if strings.HasPrefix(msg, ":") {
		// Check if already rolling or closing
		if isPokerRolling || isTriRolling || isBJRolling || is13Rolling || isHitting || is13Hitting || isClosing {
			log.Println("Already rolling or closing...")
			e.Block()
			return
		}

		command := strings.TrimPrefix(msg, ":")
		switch {
					case strings.HasPrefix(command, "session "):
			// Manual test command (Step 1):
			// :session <playerName> <itemClass> <betCount>
			// Example: :session bob duck 1
			e.Block()

			parts := strings.Fields(command) // ["session","bob","duck","1"]
			if len(parts) != 4 {
				a.AddLogMsg("Usage: :session <playerName> <itemClass> <betCount>")
				return
			}

			playerName := parts[1]
			itemClass := parts[2]
			n, err := strconv.Atoi(parts[3])
			if err != nil || n <= 0 {
				a.AddLogMsg("Session error: betCount must be a positive number.")
				return
			}

			// Don’t allow overriding an active session
			if sessionActive() {
				a.AddLogMsg("Session already active. Use :endsession first.")
				return
			}

			startSession(playerName, itemClass, n)
			a.AddLogMsg(fmt.Sprintf("Session started for %s: %dx %s. Awaiting game choice (:pkr, :tri, :21, :13)",
				playerName, n, itemClass))

		case strings.HasSuffix(command, "endsession"):
			e.Block()
			if !sessionActive() {
				a.AddLogMsg("No active session to end.")
				return
			}
			endSession()
			a.AddLogMsg("Session ended.")

		case strings.HasSuffix(command, "reset"):
			e.Block()
			resetDiceState()
		case strings.HasSuffix(command, "roll") || strings.HasSuffix(command, "pkr"):

			e.Block()
			isPokerRolling = true
			logRollResult := fmt.Sprintf("Poker Roll:\n")
			a.AddLogMsg(logRollResult)
			go a.rollPokerDice()
		case strings.HasSuffix(command, "tri"):
			e.Block()
			isTriRolling = true
			logRollResult := fmt.Sprint("Tri Roll:\n")
			a.AddLogMsg(logRollResult)
			go a.rollTriDice()
		case strings.HasSuffix(command, "close"):
			e.Block()
			go a.closeAllDice()
		case strings.HasSuffix(command, "21"):
			e.Block()
			isBJRolling = true
			logRollResult := fmt.Sprintf("21 Roll:\n")
			a.AddLogMsg(logRollResult)
			go a.rollBjDice()
		case strings.HasSuffix(command, "13"):
			e.Block()
			is13Rolling = true
			logRollResult := fmt.Sprintf("13 Roll:\n")
			a.AddLogMsg(logRollResult)
			go a.roll13Dice()
		case strings.HasPrefix(command, "@"):
			e.Block()
			extra := strings.TrimSpace(strings.TrimPrefix(command, "@"))
			go a.evalAt(extra)
		case strings.HasSuffix(command, "verify"):
			e.Block()
			go verifyResult()
		case strings.HasSuffix(command, "commands"):
			e.Block()
			go a.ShowCommands()
		case strings.HasSuffix(command, "chaton"):
			e.Block()
			ChatIsDisabled = false
		case strings.HasSuffix(command, "chatoff"):
			e.Block()
			ChatIsDisabled = true
		}
	}
}

func (a *App) evalAt(msg string) {
	mutex.Lock()
	at := "@" + msg
	ext.Send(out.SHOUT, at)
	a.AddLogMsg(at)
	mutex.Unlock()
}
func startSession(playerName, itemClass string, betCount int) {
	mutex.Lock()
	defer mutex.Unlock()

	session = Session{
		Active:             true,
		PlayerName:         playerName,
		ItemClass:          itemClass,
		BetCount:           betCount,
		Balance:            0,
		AwaitingGameChoice: true,
		InGame:             false,
		CanRisk:            false,
		CanCashOut:         false,
	}
}

func endSession() {
	mutex.Lock()
	defer mutex.Unlock()
	session = Session{}
}

func sessionActive() bool {
	mutex.Lock()
	defer mutex.Unlock()
	return session.Active
}
func countStripInstances(rawStr string, itemClass string) int {
	// Count how many item instance IDs are in this STRIPINFO_2 line.
	// In your payload, IDs look like: MjGl|MjGn|MjGHS ... HHduck
	// So count pipes BEFORE the "HH<item>" marker.

	marker := "HH" + itemClass
	idx := strings.Index(rawStr, marker)
	if idx == -1 {
		// fallback: count all pipes if marker not found
		p := strings.Count(rawStr, "|")
		if p <= 0 {
			return 1
		}
		return p + 1
	}

	prefix := rawStr[:idx]
	p := strings.Count(prefix, "|")
	if p <= 0 {
		return 1
	}
	return p + 1
}



func (a *App) refreshInventoryAndWait(timeout time.Duration) {
	// Don’t wipe invCounts here — it causes “0” windows.
	// Only GETSTRIP "new" should clear the map.
	invCollecting = true
	invReady = false
	lastStripInfoAt = time.Time{}

	// Ask server for fresh inventory
	a.ext.Send(out.GETSTRIP, []byte("AAnew"))

	end := time.Now().Add(timeout)

	// Wait until we’ve received at least one inventory packet
	for time.Now().Before(end) {
		time.Sleep(50 * time.Millisecond)

		if invReady {
			// Once ready, wait for packets to go quiet (inventory burst finished)
			if !lastStripInfoAt.IsZero() && time.Since(lastStripInfoAt) > 500*time.Millisecond {
				break
			}
		}
	}

	// IMPORTANT: do NOT set invCollecting=false here.
	// We want to keep accepting STRIPINFO_2 updates continuously.
}





// Reset all saved dice states
func resetDiceState() {
	mutex.Lock()
	defer mutex.Unlock()
	resultsWaitGroup.Wait() // Ensure all dice roll results are processed
	diceList = []*Dice{}
	isPokerRolling, isTriRolling, isBJRolling, is13Rolling, isHitting, is13Hitting, isClosing = false, false, false, false, false, false, false
}

func (a *App) handleThrowDice(e *g.Intercept) {
	packet := e.Packet
	rawData := string(packet.Data)
	logrus.WithFields(logrus.Fields{"raw_data": rawData}).Debug("Raw packet data")

	diceData := strings.Fields(rawData)
	diceIDStr := diceData[0]
	diceID, err := strconv.Atoi(diceIDStr)
	if err != nil {
		logrus.WithFields(logrus.Fields{"dice_id_str": diceIDStr, "error": err}).Warn("Failed to parse dice ID")
		return
	}

	mutex.Lock()
	defer mutex.Unlock()

	// Search for a dice with the given ID in the list
	var existingDice *Dice
	for _, dice := range diceList {
		if dice != nil && dice.ID == diceID {
			existingDice = dice
			break
		}
	}

	// If not found and the list has fewer than 5 dice, create and add a new one
	if existingDice == nil && len(diceList) < 5 {
		newDice := &Dice{ID: diceID, IsRolling: true, IsClosed: false}
		diceList = append(diceList, newDice)
		log.Printf("Dice %d added\n", diceID)

		if len(diceList) == 5 {
			message := "Dice setup sucessful! Run :roll to confirm"
			a.AddLogMsg(message)
		}
	}
}

// handle the turning off of a dice
func (a *App) handleDiceOff(e *g.Intercept) {
	packet := e.Packet
	diceIDStr := string(packet.Data)

	diceID, err := strconv.Atoi(diceIDStr)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"dice_id_str": diceIDStr,
			"error":       err,
		}).Warn("Failed to parse dice ID")
		return
	}

	mutex.Lock()
	defer mutex.Unlock()

	// Search for a dice with the given ID in the list
	var existingDice *Dice
	for _, dice := range diceList {
		if dice != nil && dice.ID == diceID {
			existingDice = dice
			break
		}
	}

	// If not found and the list has fewer than 5 dice, create and add a new one
	if existingDice == nil && len(diceList) < 5 {
		newDice := &Dice{ID: diceID, IsRolling: false, IsClosed: true}
		diceList = append(diceList, newDice)
		log.Printf("Dice %d added\n", diceID)
	}
}

// Handle the result of a dice roll
func (a *App) handleDiceResult(e *g.Intercept) {
	packet := e.Packet
	rawData := string(packet.Data)
	logrus.WithFields(logrus.Fields{"raw_data": rawData}).Debug("Raw packet data")

	diceData := strings.Fields(rawData)
	if len(diceData) < 2 {
		return
	}

	diceIDStr := diceData[0]
	diceID, err := strconv.Atoi(diceIDStr)
	if err != nil {
		logrus.WithFields(logrus.Fields{"dice_id_str": diceIDStr, "error": err}).Warn("Failed to parse dice ID")
		return
	}

	diceValueStr := diceData[1]
	diceValue, err := strconv.Atoi(diceValueStr)
	if err != nil {
		logrus.WithFields(logrus.Fields{"dice_value_str": diceValueStr, "error": err}).Warn("Failed to parse dice value")
		return
	}
	adjustedDiceValue := diceValue - (diceID * 38)

	mutex.Lock()
	for i, dice := range diceList {
		if dice.ID == diceID {
			if dice.IsRolling && (isPokerRolling || isTriRolling || isBJRolling || is13Rolling || is13Hitting || isHitting) {
				dice.IsRolling = false
				resultsWaitGroup.Done()
			}
			diceList[i].Value = adjustedDiceValue
			diceList[i].IsClosed = diceList[i].Value == 0

			if isPokerRolling || isTriRolling || isBJRolling || is13Rolling || is13Hitting || isHitting {
				log.Printf("Dice %d rolled: %d\n", diceID, adjustedDiceValue)
				logRollResult := fmt.Sprintf("Dice %d rolled: %d\n", diceID, adjustedDiceValue)
				a.AddLogMsg(logRollResult)
			}
			break
		}
	}
	mutex.Unlock()
}
// Splits packet raw bytes into human-ish tokens (Shockwave uses control separators like 0x02 and 0x7f)
func splitTokens(raw []byte) []string {
	s := string(raw)

	// Replace common separators with spaces
	replacer := strings.NewReplacer(
		"\x02", " ",
		"\x7f", " ",
		"\x00", " ",
		"\n", " ",
		"\r", " ",
		"\t", " ",
	)
	s = replacer.Replace(s)

	parts := strings.Fields(s)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}


func countInstancesBeforeHH(rawStr string, itemClass string) int {
	// Example: "...MjGl|MjGn|MjGo|MjGHS[2]i\\wBHHduck..."
	// Count pipes BEFORE "HHduck" marker in THIS packet.
	marker := "HH" + itemClass
	idx := strings.Index(rawStr, marker)
	if idx == -1 {
		// fallback: pipes in entire string
		p := strings.Count(rawStr, "|")
		if p <= 0 {
			return 1
		}
		return p + 1
	}

	prefix := rawStr[:idx]
	p := strings.Count(prefix, "|")
	if p <= 0 {
		return 1
	}
	return p + 1
}


func extractLowerWord(s string) string {
	// Find the last contiguous run of lowercase letters (e.g. "duck")
	last := ""
	cur := strings.Builder{}

	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			cur.WriteRune(r)
		} else {
			if cur.Len() >= 2 {
				last = cur.String()
			}
			cur.Reset()
		}
	}
	if cur.Len() >= 2 {
		last = cur.String()
	}

	// Normalize common Shockwave prefixes:
	// Trade tokens often contain "lduck" where the real class is "duck"
	if strings.HasPrefix(last, "l") && len(last) >= 3 {
		last = last[1:]
	}

	// Filter obvious junk that shows up in trade packets
	switch last {
	case "al", "ot", "other", "null", "trd":
		return ""
	}

	return last
}

func itemClassFromHH(raw string) string {
	// STRIPINFO_2 contains "...HHduck[2]..."
	idx := strings.Index(raw, "HH")
	if idx == -1 || idx+2 >= len(raw) {
		return ""
	}

	i := idx + 2
	j := i
	for j < len(raw) {
		c := raw[j]
		if c >= 'a' && c <= 'z' {
			j++
			continue
		}
		break
	}

	if j <= i {
		return ""
	}

	return raw[i:j]
}

func extractItemClassAndCount(tokens []string) (itemClass string, count int) {
	// Pull lowercase word candidates out of all tokens, then choose
	// the most frequent candidate (that’s usually the traded item).
	counts := map[string]int{}
	for _, t := range tokens {
		w := extractLowerWord(t)
		if w == "" {
			continue
		}
		counts[w]++
	}

	// pick best candidate: highest frequency, tie-breaker: longer word
	best := ""
	bestCount := 0
	for w, c := range counts {
		if c > bestCount || (c == bestCount && len(w) > len(best)) {
			best = w
			bestCount = c
		}
	}
	return best, bestCount
}


func pickPartnerCandidate(tokens []string) string {
	// Best-effort: pick the first token that looks like a username-ish thing.
	// This may need tuning after you see logs.
	for _, t := range tokens {
		// Avoid obvious non-names
		lt := strings.ToLower(t)
		if lt == "trd" || lt == "useradmin" || lt == "flatctrl" || lt == "null" || lt == "other" {
			continue
		}
		// allow letters, digits, underscore, hyphen
		ok := true
		for _, r := range t {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
				continue
			}
			ok = false
			break
		}
		if ok && len(t) >= 2 && len(t) <= 24 {
			return t
		}
	}
	return ""
}

func (a *App) resetTradeCapture() {
	tradeOpen = false
	tradePartner = ""
	tradeItemClass = ""
	tradeBetCount = 0
}

// Called from InterceptAll (Step 2)
func (a *App) handleTradeAndInv(e *g.Intercept) {
	h := e.Packet.Header.Value

	switch h {
		
	case 111: // TRADE_CONFIRM (Incoming) -> confirm screen shown
	if !tradeOpen {
		return
	}
	if !tradeAcceptedByBot {
		return
	}
	if tradeItemClass == "" || tradeBetCount <= 0 {
		return
	}

	needed := tradeBetCount * 2
	a.refreshInventoryAndWait(4 * time.Second)

	if !invReady {
		a.AddLogMsg("AutoConfirm skipped: inventory not ready.")
		return
	}

	have := invCounts[tradeItemClass]
	a.AddLogMsg(fmt.Sprintf("AutoConfirm check: have %d %s, need %d", have, tradeItemClass, needed))

if have >= needed {
	a.ext.Send(g.Out.Id("TRADE_CONFIRM_ACCEPT"))
	a.AddLogMsg("Trade: auto-confirmed")
}
return


case 109: // TRADE_ACCEPT (Incoming) -> player clicked accept
	if !tradeOpen {
		return
	}

	if autoTradeAccept && !tradeAcceptedByBot && tradeCanAutoAccept {
		a.ext.Send(out.TRADE_ACCEPT, []byte{})
		tradeAcceptedByBot = true
		a.AddLogMsg("Trade: auto-accepted (triggered by player accept)")
	}
	return



	case 72: // TRADE_ADDITEM (Outgoing) -> dealer is adding items
		if tradeOpen {
			dealerAddedInTrade++
		}
	return

	// ---- Inventory capture ----
	case 65: // GETSTRIP (Outgoing)
		// When client requests new inventory, reset and start collecting
		raw := string(e.Packet.Data)
		if strings.Contains(raw, "new") {
	// Only clear if we’re not already in a fresh cycle
	invCounts = map[string]int{}
	invReady = false
	invCollecting = true
	a.AddLogMsg("Inventory: collecting (GETSTRIP new)")
}

		return

	case 140: // STRIPINFO_2 (Incoming)
	rawStr := string(e.Packet.Data)

	item := itemClassFromHH(rawStr)
	if item == "" {
		return
	}

	// This packet contains a full list for that item, so set it (don’t +=)
	invCounts[item] = countInstancesBeforeHH(rawStr, item)

	invReady = true
	lastStripInfoAt = time.Now()
	return


	case 98: // STRIPINFO (Incoming)
	rawStr := string(e.Packet.Data)

	item := itemClassFromHH(rawStr)
	if item == "" {
		return
	}

	invCounts[item] = countInstancesBeforeHH(rawStr, item)

	invReady = true
	lastStripInfoAt = time.Now()
	return




	case 108: // TRADE_ITEMS (Incoming)
	if !tradeOpen {
		return
	}

	tokens := splitTokens(e.Packet.Data)

	item, total := extractItemClassAndCount(tokens)
	if item == "" || total <= 0 {
		return
	}

	// Subtract what the dealer added in this trade
	bet := total - dealerAddedInTrade
	if bet < 0 {
		bet = 0
	}

	tradeItemClass = item
	tradeBetCount = bet

	a.AddLogMsg(fmt.Sprintf("Trade: items seen => %dx %s (total=%d dealerAdded=%d)",
		tradeBetCount, tradeItemClass, total, dealerAddedInTrade))

	// Auto-accept only if we can cover payout (never accept if we can't pay)
	needed := tradeBetCount * 2
a.refreshInventoryAndWait(2 * time.Second)
have := invCounts[tradeItemClass]
a.AddLogMsg(fmt.Sprintf("DEBUG INVENTORY: %s = %d", tradeItemClass, have))
a.AddLogMsg(fmt.Sprintf("AutoAccept readiness: have %d %s, need %d", have, tradeItemClass, needed))

tradeCanAutoAccept = (have >= needed && tradeBetCount > 0 && tradeItemClass != "")

return





	case 104: // TRADE_OPEN (Incoming)
		tradeCanAutoAccept = false
		tradeNeeded = 0
		dealerAddedInTrade = 0
		tradeOpen = true
		tradeAcceptedByBot = false
		tradeItemClass = ""
		tradeBetCount = 0
		tradePartner = pickPartnerCandidate(splitTokens(e.Packet.Data))
		a.AddLogMsg("Trade: opened")
	return



	case 112: // TRADE_COMPLETED (Incoming)
		if !tradeOpen {
			return
		}

		// End trade capture state
		tradeOpen = false

		// Don't start if session already active
		if sessionActive() {
			a.AddLogMsg("Trade: completed but session already active (ignored)")
			a.resetTradeCapture()
			return
		}

		// Need item + count at minimum
		if tradeItemClass == "" || tradeBetCount <= 0 {
			a.AddLogMsg("Trade: completed but could not detect item/bet (ignored)")
			a.resetTradeCapture()
			return
		}

		// Partner name is best-effort; if empty, we still start but mark unknown
		playerName := tradePartner
		if strings.TrimSpace(playerName) == "" {
			playerName = "UNKNOWN_PLAYER"
		}

needed := tradeBetCount * 2

a.refreshInventoryAndWait(4 * time.Second)

have := invCounts[tradeItemClass]
a.AddLogMsg(fmt.Sprintf("Payout check: have %d %s, need %d", have, tradeItemClass, needed))

// If inventory never updated, don't trust have=0
	if !invReady {
    a.AddLogMsg("Payout check failed: inventory not ready yet (no STRIPINFO_2 received). Denying bet.")
	a.logAndMaybeShout("Session denied", "Inventory not ready. Please retry trade.")
	a.resetTradeCapture()
	return
}

if have < needed {
	a.AddLogMsg(fmt.Sprintf("Session denied: need %d %s to cover payout, have %d", needed, tradeItemClass, have))
	a.logAndMaybeShout("Session denied", fmt.Sprintf("Can't cover payout for %s (%d needed).", tradeItemClass, needed))
	a.resetTradeCapture()
	return
}


		// Start session
		startSession(playerName, tradeItemClass, tradeBetCount)

		a.AddLogMsg(fmt.Sprintf("Session started via trade: %s bet %dx %s", playerName, tradeBetCount, tradeItemClass))
		a.logAndMaybeShout(
			"Session started",
			fmt.Sprintf("%s bet %d %s. Choose game: :pkr, :tri, :21, :13", playerName, tradeBetCount, tradeItemClass),
		)

		a.resetTradeCapture()
		return

	case 110: // TRADE_CLOSE (Incoming)
		// Trade aborted/closed, clear capture
		dealerAddedInTrade = 0
		if tradeOpen {
			a.AddLogMsg("Trade: closed")
		}
		a.resetTradeCapture()
		tradeAcceptedByBot = false
		return
	}
}

// Close the dice and send the packets to the game server
func (a *App) closeAllDice() {
	mutex.Lock()
	isClosing = true
	mutex.Unlock()

	for _, dice := range diceList {
		dice.Close()

		// random delay between 550 and 600ms
		time.Sleep(rollDelay + time.Duration(rand.Intn(50))*time.Millisecond)
	}
	mutex.Lock()
	isClosing = false
	mutex.Unlock()
}

// Roll the poker dice by sending packets and waiting for results
func (a *App) rollPokerDice() {
	mutex.Lock()

	if len(diceList) < 5 {
		mutex.Unlock()
		log.Println("Not enough dice to roll")
		isPokerRolling = false
		return
	}

	for _, dice := range diceList {
		dice.IsRolling = false
	}
	resultsWaitGroup.Add(len(diceList))
	mutex.Unlock()

	for _, dice := range diceList {
		dice.Roll()

		// random delay between 550 and 600ms
		time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
	}

	time.Sleep(1000 * time.Millisecond)
	if !waitForResults(5 * time.Second) {
		a.AddLogMsg("Poker roll timed out waiting for dice results")
		isPokerRolling = false
		return
	}
	a.evaluatePokerRound()
}

func waitForResults(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		resultsWaitGroup.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		mutex.Lock()
		resultsWaitGroup = sync.WaitGroup{}
		for _, dice := range diceList {
			dice.IsRolling = false
		}
		mutex.Unlock()
		return false
	}
}

// Evaluate the poker hand and send the result to the chat
func (a *App) rollTriDice() {
	mutex.Lock()

	if len(diceList) < 5 {
		mutex.Unlock()
		log.Println("Not enough dice to roll")
		isTriRolling = false
		return
	}

	resultsWaitGroup.Add(3)
	mutex.Unlock()

	for _, index := range []int{0, 2, 4} {
		diceList[index].Roll()
		time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
	}

	time.Sleep(1000 * time.Millisecond)
	resultsWaitGroup.Wait()

	a.evaluateTriHand()
	isTriRolling = false
}

// Roll dice for blackjack-style game
func (a *App) rollBjDice() {
	go a.closeAllDice()
	time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
	mutex.Lock()

	if len(diceList) < 5 {
		mutex.Unlock()
		log.Println("Not enough dice to roll")
		isBJRolling = false
		return
	}

	currentSum = 0 // Reset sum before starting
	resultsWaitGroup.Add(3)
	mutex.Unlock()

	// Roll the first three dice in order
	for _, index := range []int{0, 1, 2} {
		diceList[index].Roll()
		time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
	}

	time.Sleep(1000 * time.Millisecond)
	resultsWaitGroup.Wait()

	mutex.Lock()
	for _, index := range []int{0, 1, 2} {
		currentSum += diceList[index].Value
	}
	mutex.Unlock()

	a.evaluateBlackjackHand()
	isBJRolling = false
}

func (a *App) hitBjDice() {
	mutex.Lock()

	if len(diceList) < 5 {
		mutex.Unlock()
		log.Println("Not enough dice to roll")
		isBJRolling = false
		isHitting = false
		return
	}

	resultsWaitGroup.Add(1)
	mutex.Unlock()

	for i := 3; i < 5; i++ { // Start from index 3 to roll the next available dice
		if diceList[i].Value == 0 {
			diceList[i].Roll()
			time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
			resultsWaitGroup.Wait()

			mutex.Lock()
			currentSum += diceList[i].Value // Add value to current sum
			mutex.Unlock()

			// Re-evaluate the hand after hitting
			a.evaluateBlackjackHand()

			isBJRolling = false
			isHitting = false
			return
		}
	}

	// If all dice have been rolled, re-roll the last one
	oldValue := diceList[4].Value
	// sleep random between 1000 and 1500ms
	time.Sleep(time.Duration(rand.Intn(1000)+500) * time.Millisecond)
	diceList[4].Roll()

	time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
	resultsWaitGroup.Wait()
	newValue := diceList[4].Value

	mutex.Lock()
	currentSum = currentSum + newValue // Adjust current sum
	mutex.Unlock()

	// Log the value of the dice rolled
	log.Printf("Hit: Re-rolled dice %d = %d (old value was %d)\n", diceList[4].ID, newValue, oldValue)

	// Re-evaluate the hand with the updated sum
	a.evaluateBlackjackHand()

	isHitting = false
	isBJRolling = false
}

// Roll dice for blackjack-style game
func (a *App) roll13Dice() {
	go a.closeAllDice()
	time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
	mutex.Lock()

	if len(diceList) < 5 {
		mutex.Unlock()
		log.Println("Not enough dice to roll")
		is13Rolling = false
		return
	}

	currentSum = 0 // Reset sum before starting
	resultsWaitGroup.Add(2)
	mutex.Unlock()

	// Roll the first three dice in order
	for _, index := range []int{0, 1} {
		diceList[index].Roll()
		time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
	}

	time.Sleep(1000 * time.Millisecond)
	resultsWaitGroup.Wait()

	mutex.Lock()
	for _, index := range []int{0, 1} {
		currentSum += diceList[index].Value
	}
	mutex.Unlock()

	a.evaluate13Hand()
	is13Rolling = false
}

func (a *App) hit13Dice() {
	mutex.Lock()

	if len(diceList) < 5 {
		mutex.Unlock()
		log.Println("Not enough dice to roll")
		is13Rolling = false
		is13Hitting = false
		return
	}

	resultsWaitGroup.Add(1)
	mutex.Unlock()

	for i := 2; i < 5; i++ { // Start from index 2 to roll the next available dice
		if diceList[i].Value == 0 {
			diceList[i].Roll()
			time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
			resultsWaitGroup.Wait()

			mutex.Lock()
			currentSum += diceList[i].Value // Add value to current sum
			mutex.Unlock()

			// Re-evaluate the hand after hitting
			a.evaluate13Hand()

			is13Rolling = false
			is13Hitting = false
			return
		}
	}

	// If all dice have been rolled, re-roll the last one
	oldValue := diceList[4].Value
	// sleep random between 1000 and 1500ms
	time.Sleep(time.Duration(rand.Intn(1000)+500) * time.Millisecond)
	diceList[4].Roll()

	time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
	resultsWaitGroup.Wait()
	newValue := diceList[4].Value

	mutex.Lock()
	currentSum = currentSum + newValue // Adjust current sum
	mutex.Unlock()

	// Log the value of the dice rolled
	log.Printf("Hit: Re-rolled dice %d = %d (old value was %d)\n", diceList[4].ID, newValue, oldValue)

	// Re-evaluate the hand with the updated sum
	a.evaluate13Hand()

	is13Hitting = false
	is13Rolling = false
}

func verifyResult() {
	// Convert the currentSum to a string
	sumStr := strconv.Itoa(currentSum)
	mutex.Lock()
	ext.Send(out.SHOUT, sumStr)
	mutex.Unlock()
}

func (a *App) ShowCommands() {
	commandList :=
		"Thanks for using my plugin!\nBelow is it's list of commands. \n" +
			"------------------------------------\n" +
			":reset \n" +
			"Forgets dice list for when you\nchange booth.\n" +
			"------------------------------------\n" +
			":roll \n" +
			"Rolls 5 dice and if chat is enabled \nsays the results in chat. \n" +
			"------------------------------------\n" +
			":close\n" +
			"Closes any of your open dice. \n" +
			"------------------------------------\n" +
			":21 \n" +
			"Auto rolls and if chat is enabled \nsays the sum in chat when > 15. \n" +
			"------------------------------------\n" +
			":13 \n" +
			"Auto rolls and if chat is enabled \nsays the sum in chat when > 8. \n" +
			"------------------------------------\n" +
			":tri \n" +
			"Auto rolls 3 dice in Tri Formation \nif chat is enabled says the \nresults in chat. \n" +
			"------------------------------------\n" +
			":verify \n" +
			"Will say the previous result in\nchat. Use if you were muted and\ndont know the results of 21/13.\n" +
			"------------------------------------\n" +
			":chaton \n" +
			"Enables chat announcement \nof game results. \n" +
			"------------------------------------\n" +
			":chatoff \n" +
			"Disables chat announcement \nof game results. \n" +
			"------------------------------------\n" +
			":@ <amount> \n" +
			"Stores @ amount in roll log \nwith the result and will announce \nit in chat. \n" +
			"------------------------------------\n" +
			":session <player> <item> <count>\n" +
			"Manual test: starts a session.\n" +
			"------------------------------------\n" +
			":endsession\n" +
			"Ends the current session.\n" +
			"------------------------------------\n" +
			":commands - This help screen :)"

	// IMPORTANT: Sleep must be a standalone statement, NOT inside the string concatenation.
	time.Sleep(time.Duration(rand.Intn(250)+250) * time.Millisecond)
	ext.Send(in.SYSTEM_BROADCAST, commandList)
}


// QDave's Logging function for frontend
func (a *App) AddLogMsg(msg string) {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	// Get the current time and format it as a timestamp
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	// Prepend the timestamp to the message
	timestampedMsg := fmt.Sprintf("[%s] %s", timestamp, msg)

	a.log = append(a.log, timestampedMsg)
	if len(a.log) > 100 {
		a.log = a.log[1:]
	}
	runtime.EventsEmit(a.ctx, "logUpdate", strings.Join(a.log, "\n"))
}

// Thanks QDave <3
func (a *App) handleTalk(e *g.Intercept) {
	msg := e.Packet.ReadString()
	if msg == "#gsuite" {
		runtime.WindowShow(a.ctx)
		e.Block()
	}
}
