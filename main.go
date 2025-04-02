package main

import (
	"context"
	"embed"
	"encoding/binary"
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

	// Flash (Legacy) version
	legacyIn "xabbo.b7c.io/goearth/in"
	legacyOut "xabbo.b7c.io/goearth/out"
)

// Global variables for dice management, rolling state, mutex, and wait group
var (
	diceList         []*Dice
	mutedDuration    int
	isMuted          bool
	currentSum       int
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
	isFlash          *bool // Flag to check if user is running Origins or Legacy (.com)
)

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
			TwoPair:      "Two Pair: %ss",
			OnePair:      "One Pair: %ss",
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
	configPath := filepath.Join(configDir, "Gamba-Suite")
	os.MkdirAll(configPath, 0700)
	return filepath.Join(configPath, "poker_display_config.json")
}

func (a *App) setupExt() {
	go func() { // Run in a separate goroutine
		for isFlash == nil { // Wait until isFlash is set
			time.Sleep(100 * time.Millisecond) // Small delay to prevent CPU overuse
		}
		if isFlash != nil && !*isFlash {
			log.Printf("Detected Origins: %v", *isFlash)
			a.ext.Intercept(out.CHAT, out.SHOUT, out.WHISPER).With(a.onChatMessage)
			a.ext.Intercept(out.THROW_DICE).With(a.handleThrowDice)
			a.ext.Intercept(out.DICE_OFF).With(a.handleDiceOff)
			a.ext.Intercept(in.DICE_VALUE).With(a.handleDiceResult)
			a.ext.Intercept(out.CHAT).With(a.handleTalk)
			a.ext.Intercept(out.SHOUT).With(a.handleTalk)
			a.ext.InterceptAll(func(e *g.Intercept) {
				handleMutePacket(e)
			})
		} else {
			log.Printf("Detected Flash: %v", *isFlash)
			a.ext.Intercept(legacyOut.Chat, legacyOut.Shout, legacyOut.Whisper).With(a.onChatMessage)
			a.ext.Intercept(legacyOut.ThrowDice).With(a.handleThrowDice)
			a.ext.Intercept(legacyOut.DiceOff).With(a.handleDiceOff)
			a.ext.Intercept(legacyIn.DiceValue).With(a.handleDiceResult)
			a.ext.Intercept(legacyOut.Chat).With(a.handleTalk)
			a.ext.Intercept(legacyOut.Shout).With(a.handleTalk)
		}
	}()
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
		case strings.HasSuffix(command, "reset"):
			e.Block()
			resetDiceState()
		case strings.HasSuffix(command, "roll"):
			e.Block()
			isPokerRolling = true
			logRollResult := fmt.Sprintln("Poker Roll:")
			a.AddLogMsg(logRollResult)
			if isFlash != nil && !*isFlash { // If it's Origins
				go a.rollPokerDice()
			} else {
				go a.rollPokerDiceFlash()
			}
		case strings.HasSuffix(command, "tri"):
			e.Block()
			isTriRolling = true
			logRollResult := fmt.Sprintln("Tri Roll:")
			a.AddLogMsg(logRollResult)
			go a.rollTriDice()
		case strings.HasSuffix(command, "close"):
			e.Block()
			go a.closeAllDice()
		case strings.HasSuffix(command, "21"):
			e.Block()
			isBJRolling = true
			logRollResult := fmt.Sprintln("21 Roll:")
			a.AddLogMsg(logRollResult)
			go a.rollBjDice()
		case strings.HasSuffix(command, "13"):
			e.Block()
			is13Rolling = true
			logRollResult := fmt.Sprintln("13 Roll:")
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

	var diceID int
	var err error

	if isFlash != nil && *isFlash { // Flash (Legacy) client handling
		// Flash uses binary data, so we need to extract the integer correctly.
		if len(packet.Data) >= 4 { // Ensure at least 4 bytes are available
			diceID = int(binary.BigEndian.Uint32(packet.Data[len(packet.Data)-4:])) // Extract last 4 bytes as int
		} else {
			log.Println("Invalid Flash packet format:", packet.Data)
			return
		}
	} else { // Origins (Shockwave) client handling
		rawData := string(packet.Data)
		log.Println("RawData:", rawData)

		diceData := strings.Fields(rawData)
		if len(diceData) == 0 {
			log.Println("Invalid Origins packet format")
			return
		}

		diceIDStr := diceData[0]
		diceID, err = strconv.Atoi(diceIDStr)
		if err != nil {
			logrus.WithFields(logrus.Fields{"dice_id_str": diceIDStr, "error": err}).Warn("Failed to parse dice ID")
			return
		}
	}

	log.Printf("Parsed Dice ID: %d", diceID)

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

	if !*isFlash {
		// Origins allows up to 5 dice
		if existingDice == nil && len(diceList) < 5 {
			newDice := &Dice{ID: diceID, IsRolling: true, IsClosed: false}
			diceList = append(diceList, newDice)
			log.Printf("Dice %d added\n", diceID)

			if len(diceList) == 5 {
				message := "Dice setup successful! Run :roll to confirm"
				a.AddLogMsg(message)
			}
		}
	} else {
		// Flash allows only 3 dice
		if existingDice == nil && len(diceList) < 3 {
			newDice := &Dice{ID: diceID, IsRolling: true, IsClosed: false}
			diceList = append(diceList, newDice)
			log.Printf("Dice %d added\n", diceID)

			if len(diceList) == 3 {
				message := "Dice setup successful! Run :roll to confirm"
				a.AddLogMsg(message)
			}
		}
	}
}

// handle the turning off of a dice
func (a *App) handleDiceOff(e *g.Intercept) {
	packet := e.Packet
	var diceID int
	var err error

	if isFlash != nil && *isFlash { // Flash (Legacy) client handling
		// Flash uses raw binary data, extract the dice ID (first 4 bytes)
		if len(packet.Data) >= 4 { // Ensure there are at least 4 bytes
			diceID = int(binary.BigEndian.Uint32(packet.Data[:4])) // Extract first 4 bytes as an integer
		} else {
			log.Println("Invalid Flash packet format:", packet.Data)
			return
		}
	} else { // Origins (Shockwave) client handling
		diceIDStr := string(packet.Data)
		diceID, err = strconv.Atoi(diceIDStr)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"dice_id_str": diceIDStr,
				"error":       err,
			}).Warn("Failed to parse dice ID")
			return
		}
	}

	log.Printf("Parsed Dice ID (isFlash: %v): %d", isFlash, diceID)

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

	// If not found, add a new dice entry
	maxDice := 5
	if isFlash != nil && *isFlash {
		maxDice = 3 // Flash clients use up to 3 dice
	}

	if existingDice == nil && len(diceList) < maxDice {
		newDice := &Dice{ID: diceID, IsRolling: false, IsClosed: true}
		diceList = append(diceList, newDice)
		log.Printf("Dice %d added\n", diceID)
	}
}

// Handle the result of a dice roll
func (a *App) handleDiceResult(e *g.Intercept) {
	packet := e.Packet
	logrus.WithFields(logrus.Fields{"packet_data": packet.Data}).Debug("Packet data received")

	var diceID int
	var diceValue int
	var err error

	if isFlash != nil && *isFlash { // Flash (Legacy) client handling
		// Flash uses raw binary data, extract diceID (first 4 bytes) and diceValue (last byte)
		if len(packet.Data) >= 5 { // Ensure at least 5 bytes: 4 for ID, 1 for value
			diceID = int(binary.BigEndian.Uint32(packet.Data[:4])) // Extract first 4 bytes for dice ID
			diceValue = int(packet.Data[len(packet.Data)-1])       // Extract the last byte for dice value

			// Ignore rolling state placeholder (255)
			if diceValue == 255 || diceValue == 100 {
				log.Printf("Dice %d is still rolling...", diceID)
				return
			}
		} else {
			log.Println("Invalid Flash packet format:", packet.Data)
			return
		}
	} else { // Shockwave (Origins) client handling
		rawData := string(packet.Data)
		logrus.WithFields(logrus.Fields{"raw_data": rawData}).Debug("Raw packet data")

		diceData := strings.Fields(rawData)
		if len(diceData) < 2 {
			return
		}

		// Parse dice ID and dice value from the string data
		diceIDStr := diceData[0]
		diceID, err = strconv.Atoi(diceIDStr)
		if err != nil {
			logrus.WithFields(logrus.Fields{"dice_id_str": diceIDStr, "error": err}).Warn("Failed to parse dice ID")
			return
		}

		diceValueStr := diceData[1]
		diceValue, err = strconv.Atoi(diceValueStr)
		if err != nil {
			logrus.WithFields(logrus.Fields{"dice_value_str": diceValueStr, "error": err}).Warn("Failed to parse dice value")
			return
		}
	}

	// Adjust dice value based on client type
	adjustedDiceValue := diceValue
	if isFlash != nil && !*isFlash { // If it's Origins
		adjustedDiceValue = diceValue - (diceID * 38)
	}

	// // Log the dice roll result
	// log.Printf("Dice %d rolled: %d\n", diceID, adjustedDiceValue)
	// logRollResult := fmt.Sprintf("Dice %d rolled: %d\n", diceID, adjustedDiceValue)
	// a.AddLogMsg(logRollResult)

	// Update dice result in the dice list
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

	resultsWaitGroup.Add(len(diceList))
	mutex.Unlock()

	for _, dice := range diceList {
		dice.Roll()

		// random delay between 550 and 600ms
		time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
	}

	time.Sleep(1000 * time.Millisecond)
	resultsWaitGroup.Wait()
	a.evaluatePokerHand()
	isPokerRolling = false
}

// Roll the poker dice by sending packets and waiting for results
func (a *App) rollPokerDiceFlash() {
	mutex.Lock()

	// Flash allows only 3 dice
	if len(diceList) < 3 {
		mutex.Unlock()
		log.Println("Not enough dice to roll")
		isPokerRolling = false
		return
	}

	resultsWaitGroup.Add(3) // Add the total number of rolls to the wait group
	mutex.Unlock()

	// Flash version: Roll 3 dice first
	flashDiceResults := make([]int, 5)

	// Ensure it is reset (just to be explicit)
	for i := range flashDiceResults {
		flashDiceResults[i] = 0
	}

	for _, dice := range diceList {
		dice.Roll()
		// random delay between 550 and 600ms
		time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
		// log.Printf("debug 1 = %v", diceList[i].Value)
		// flashDiceResults[i] = diceList[i].Value
	}

	time.Sleep(1000 * time.Millisecond)
	resultsWaitGroup.Wait()
	for i, _ := range diceList {
		log.Printf("debug 1 = %v", diceList[i].Value)
		flashDiceResults[i] = diceList[i].Value
	}

	// Debug output to confirm stored values
	log.Printf("First 3 dice results: %v\n", flashDiceResults[:3])

	// Short delay before re-rolling the first two dice
	time.Sleep(500 * time.Millisecond)
	time.Sleep(rollDelay + time.Duration(rand.Intn(250))*time.Millisecond)

	mutex.Lock()
	resultsWaitGroup.Add(2) // Add the additional dice to wait group
	mutex.Unlock()

	// Only re-roll for dice at index 0 and 1 (for indices 3 and 4)
	for _, index := range []int{0, 1} {
		diceList[index].Roll()
		time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
		// flashDiceResults[3+index] = diceList[index].Value // Correctly update the 4th and 5th elements
	}

	time.Sleep(1000 * time.Millisecond)
	resultsWaitGroup.Wait()
	time.Sleep(rollDelay + time.Duration(rand.Intn(250))*time.Millisecond)
	// After waiting for the re-roll, update the second part of the results
	for i, index := range []int{0, 1} {
		log.Printf("debug 2 = %v", diceList[index].Value) // Log the value
		flashDiceResults[3+i] = diceList[index].Value     // Correctly update the 4th and 5th elements
	}
	// for id, _ := range []int{0, 1} {
	// 	for i := 3; i < 5; i++ {
	// 		log.Printf("debug 2 = %v", diceList[id].Value)
	// 		flashDiceResults[i] = diceList[id].Value
	// 	}
	// }

	// Debug output to confirm stored values
	log.Printf("Final Flash Dice Results: %v\n", flashDiceResults)

	// for i := 0; i < 2; i++ {
	// 	resultsWaitGroup.Add(1) // Add the total number of rolls to the wait group
	// 	diceList[i].Roll()
	// 	flashDiceResults[3+i] = diceList[i].Value
	// 	time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
	// 	resultsWaitGroup.Done() // Mark the roll as complete
	// }

	// Debug output to confirm stored values
	log.Printf("Final Flash Dice Results: %v\n", flashDiceResults)

	// Use the stored values for evaluation
	a.evaluatePokerHandFlash(flashDiceResults)

	isPokerRolling = false
}

func (a *App) rollTriDice() {
	mutex.Lock()

	if !*isFlash {
		// Origins allows up to 5 dice
		if len(diceList) < 5 {
			mutex.Unlock()
			log.Println("Not enough dice to roll")
			isTriRolling = false
			return
		}
	} else {
		// Flash allows only 3 dice
		if len(diceList) < 3 {
			mutex.Unlock()
			log.Println("Not enough dice to roll")
			isTriRolling = false
			return
		}
	}

	resultsWaitGroup.Add(3)
	mutex.Unlock()

	if !*isFlash {
		// Origins allows up to 5 dice, roll in triangle formation.
		for _, index := range []int{0, 2, 4} {
			diceList[index].Roll()
			time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
		}
	} else {
		// Flash only allows up to 3 dice, roll in ascending order.
		for _, index := range []int{0, 1, 2} {
			diceList[index].Roll()
			time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
		}
	}

	time.Sleep(1000 * time.Millisecond)
	resultsWaitGroup.Wait()

	a.evaluateTriHand()
	isTriRolling = false
}

var nextDiceIndex int // Circular Rolling Counter

// Roll dice for blackjack-style game
func (a *App) rollBjDice() {
	go a.closeAllDice()
	time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
	mutex.Lock()

	if isFlash != nil && *isFlash {
		if len(diceList) < 3 {
			mutex.Unlock()
			log.Println("Not enough dice to roll for Flash")
			isBJRolling = false
			return
		}
	} else {
		if len(diceList) < 5 {
			mutex.Unlock()
			log.Println("Not enough dice to roll for Origins")
			isBJRolling = false
			return
		}
	}

	currentSum = 0 // Reset sum before starting
	resultsWaitGroup.Add(3)
	mutex.Unlock()

	nextDiceIndex = 0 // Reset state

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

	maxDice := 5 // Default to Origins (5 dice)
	if isFlash != nil && *isFlash {
		maxDice = 3 // Flash has only 3 dice
	}

	if len(diceList) < maxDice {
		mutex.Unlock()
		log.Printf("Not enough dice to hit for %s\n", func() string {
			if maxDice == 3 {
				return "Flash"
			}
			return "Origins"
		}())
		isBJRolling = false
		isHitting = false
		return
	}

	resultsWaitGroup.Add(1)
	mutex.Unlock()

	// Try rolling the next available unrolled dice
	for i := 3; i < maxDice; i++ {
		if diceList[i].Value == 0 {
			diceList[i].Roll()
			time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
			resultsWaitGroup.Wait()

			mutex.Lock()
			currentSum += diceList[i].Value
			mutex.Unlock()

			a.evaluateBlackjackHand()
			isBJRolling = false
			isHitting = false
			return
		}
	}

	// If all dice have been rolled, start re-rolling the oldest one
	oldValue := diceList[nextDiceIndex%maxDice].Value
	time.Sleep(time.Duration(rand.Intn(1000)+500) * time.Millisecond)
	diceList[nextDiceIndex%maxDice].Roll()

	time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
	resultsWaitGroup.Wait()
	newValue := diceList[nextDiceIndex%maxDice].Value

	mutex.Lock()
	currentSum = currentSum + newValue
	mutex.Unlock()

	log.Printf("Hit: Re-rolled dice %d = %d (old value was %d)\n", diceList[nextDiceIndex%maxDice].ID, newValue, oldValue)

	nextDiceIndex = (nextDiceIndex + 1) % maxDice

	a.evaluateBlackjackHand()

	isHitting = false
	isBJRolling = false
}

// Roll dice for blackjack-style game
func (a *App) roll13Dice() {
	go a.closeAllDice()
	time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
	mutex.Lock()

	if isFlash != nil && *isFlash {
		if len(diceList) < 3 {
			mutex.Unlock()
			log.Println("Not enough dice to roll for Flash")
			is13Rolling = false
			return
		}
	} else {
		if len(diceList) < 5 {
			mutex.Unlock()
			log.Println("Not enough dice to roll for Origins")
			is13Rolling = false
			return
		}
	}

	currentSum = 0 // Reset sum before starting
	resultsWaitGroup.Add(2)
	mutex.Unlock()

	nextDiceIndex = 2 // Reset state (default to 2 since we will always roll first 2 Dice in 13)

	// Roll the first two dice in order
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

	maxDice := 5 // Default to Origins (5 dice)
	if isFlash != nil && *isFlash {
		maxDice = 3 // Flash has only 3 dice
	}

	if len(diceList) < maxDice {
		mutex.Unlock()
		log.Printf("Not enough dice to hit for %s\n", func() string {
			if maxDice == 3 {
				return "Flash"
			}
			return "Origins"
		}())
		is13Rolling = false
		is13Hitting = false
		return
	}

	resultsWaitGroup.Add(1)
	mutex.Unlock()

	for i := 2; i < maxDice; i++ { // Start from index 2 to roll the next available dice
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
	oldValue := diceList[nextDiceIndex%maxDice].Value
	// sleep random between 1000 and 1500ms
	time.Sleep(time.Duration(rand.Intn(1000)+500) * time.Millisecond)
	diceList[nextDiceIndex%maxDice].Roll()

	time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
	resultsWaitGroup.Wait()
	newValue := diceList[nextDiceIndex%maxDice].Value

	mutex.Lock()
	currentSum = currentSum + newValue // Adjust current sum
	mutex.Unlock()

	// Log the value of the dice rolled
	log.Printf("Hit: Re-rolled dice %d = %d (old value was %d)\n", diceList[nextDiceIndex%maxDice].ID, newValue, oldValue)

	nextDiceIndex = (nextDiceIndex + 1) % maxDice

	// Re-evaluate the hand with the updated sum
	a.evaluate13Hand()

	is13Hitting = false
	is13Rolling = false
}

func verifyResult() {
	// Convert the currentSum to a string
	sumStr := strconv.Itoa(currentSum)
	mutex.Lock()
	if isFlash != nil && *isFlash { // Flash (Legacy) client handling
		ext.Send(legacyOut.Shout, sumStr)
	} else { // Origins (Shockwave) client handling
		ext.Send(out.SHOUT, sumStr)
	}
	mutex.Unlock()
}

func (a *App) HandleAction(action string) {
	// Ensure that no action is being executed concurrently
	if isPokerRolling || isTriRolling || isBJRolling || is13Rolling || isHitting || is13Hitting || isClosing {
		log.Println("Already rolling or closing...")
		return
	}

	// Switch case to handle different actions
	switch action {
	case "poker":
		logRollResult := fmt.Sprintln("Poker Roll:")
		a.AddLogMsg(logRollResult)
		isPokerRolling = true
		if isFlash != nil && !*isFlash { // If it's Origins
			go a.rollPokerDice()
		} else {
			go a.rollPokerDiceFlash()
		}

	case "tri":
		logRollResult := fmt.Sprintln("Tri Roll:")
		a.AddLogMsg(logRollResult)
		isTriRolling = true
		go a.rollTriDice()

	case "21":
		logRollResult := fmt.Sprintln("21 Roll:")
		a.AddLogMsg(logRollResult)
		isBJRolling = true
		go a.rollBjDice()

	case "13":
		logRollResult := fmt.Sprintln("13 Roll:")
		a.AddLogMsg(logRollResult)
		is13Rolling = true
		go a.roll13Dice()

	default:
		log.Printf("Unknown action: %s", action)
	}
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
			":commands - This help screen :)"

	time.Sleep(time.Duration(rand.Intn(250)+250) * time.Millisecond)
	if isFlash != nil && *isFlash { // Flash (Legacy) client handling
		ext.Send(legacyIn.HabboBroadcast, commandList)
	} else { // Origins (Shockwave) client handling
		ext.Send(in.SYSTEM_BROADCAST, commandList)
	}
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
