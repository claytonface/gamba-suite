package main

import (
	"fmt"
	"log"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"time"

	"xabbo.b7c.io/goearth/shockwave/out"
)

// Send message with a delay to simulate user typing/waiting
func sendMessageWithDelay(message string) {
	// sleep random between 250 and 500ms
	time.Sleep(time.Duration(rand.Intn(250)+250) * time.Millisecond)
	ext.Send(out.SHOUT, message)
	log.Printf("Sent message: %s", message)
}

// Wait for all dice results and evaluate the poker hand
func (a *App) evaluatePokerHand() {
	if !ChatIsDisabled {
		hand := a.toPokerHandResult(diceList)
		logRollResult := fmt.Sprintf("Poker Result: %s\n", hand.Description)
		time.Sleep(time.Duration(rand.Intn(250)+250) * time.Millisecond)
		a.AddLogMsg(logRollResult)

		if !isMuted {
			// If the user is not muted, send the message
			sendMessageWithDelay(hand.Description)
		} else {
			// If the user is muted, queue the message to send later
			log.Printf("User is muted. Queuing message: %s", hand.Description)
			// ToDo:
			// messageQueue = append(messageQueue, hand)
		}
	} else {
		hand := a.toPokerHandResult(diceList)
		logRollResult := fmt.Sprintf("Poker Result: %s\n", hand.Description)
		time.Sleep(time.Duration(rand.Intn(250)+250) * time.Millisecond)
		a.AddLogMsg(logRollResult)
	}
	isPokerRolling = false
}

func (a *App) evaluatePokerRound() {
	playerHand := a.toPokerHandResult(diceList)
	playerMessage := fmt.Sprintf("Player has %s %s", playerHand.Description, playerHand.DiceString())
	a.logAndMaybeShout("Poker Result: "+playerMessage, playerMessage)

	time.Sleep(3 * time.Second)

	resultsWaitGroup.Add(len(diceList))
	for _, dice := range diceList {
		dice.Roll()
		time.Sleep(rollDelay + time.Duration(rand.Intn(100))*time.Millisecond)
	}

	time.Sleep(1000 * time.Millisecond)
	resultsWaitGroup.Wait()

	dealerHand := a.toPokerHandResult(diceList)
	dealerMessage := fmt.Sprintf("Dealer has %s %s", dealerHand.Description, dealerHand.DiceString())
	a.logAndMaybeShout("Poker Result: "+dealerMessage, dealerMessage)

	resultMessage := comparePokerHands(playerHand, dealerHand)
	a.logAndMaybeShout("Poker Result: "+resultMessage, resultMessage)
	isPokerRolling = false
}

func (a *App) evaluateBlackjackHand() {
	mutex.Lock()
	mutex.Unlock()

	if !ChatIsDisabled {
		// Log the current sum for debugging purposes
		log.Printf("Evaluating hand: Current sum = %d\n", currentSum)

		// If sum is less than 15, call hitBjDice to roll another dice
		if currentSum < 15 {
			log.Println("Sum is less than 15. Hitting another dice.")
			a.hitBjDice() // This will hit the dice and then re-evaluate the hand
			return        // Return early after hitting, so we don't send a message yet
		}

		// Convert sum to string and send to chat
		hand := strconv.Itoa(currentSum)
		logRollResult := fmt.Sprintf("21 Result: %s\n", hand)
		time.Sleep(time.Duration(rand.Intn(250)+250) * time.Millisecond)
		a.AddLogMsg(logRollResult)

		if !isMuted {
			// If the user is not muted, send the message
			sendMessageWithDelay(hand)
		} else {
			// If the user is muted, queue the message to send later
			log.Printf("User is muted. Queuing message: %s", hand)
			// ToDo:
			// messageQueue = append(messageQueue, hand)
		}
	} else {
		// Log the current sum for debugging purposes
		log.Printf("Evaluating hand: Current sum = %d\n", currentSum)

		// If sum is less than 15, call hitBjDice to roll another dice
		if currentSum < 15 {
			log.Println("Sum is less than 15. Hitting another dice.")
			a.hitBjDice() // This will hit the dice and then re-evaluate the hand
			return        // Return early after hitting, so we don't send a message yet
		}

		// Convert sum to string and send to chat
		hand := strconv.Itoa(currentSum)
		logRollResult := fmt.Sprintf("21 Result: %s\n", hand)
		time.Sleep(time.Duration(rand.Intn(250)+250) * time.Millisecond)
		a.AddLogMsg(logRollResult)
	}
	isBJRolling = false
	isHitting = false
}

func (a *App) evaluate13Hand() {
	mutex.Lock()
	mutex.Unlock()

	if !ChatIsDisabled {
		// Log the current sum for debugging purposes
		log.Printf("Evaluating hand: Current sum = %d\n", currentSum)

		// If sum is less than 15, call hitBjDice to roll another dice
		if currentSum < 7 {
			log.Println("Sum is less than 7. Hitting another dice.")
			a.hit13Dice() // This will hit the dice and then re-evaluate the hand
			return        // Return early after hitting, so we don't send a message yet
		}

		// Convert sum to string and send to chat
		hand := strconv.Itoa(currentSum)
		logRollResult := fmt.Sprintf("13 Result: %s\n", hand)
		time.Sleep(time.Duration(rand.Intn(250)+250) * time.Millisecond)
		a.AddLogMsg(logRollResult)
		if !isMuted {
			// If the user is not muted, send the message
			sendMessageWithDelay(hand)
		} else {
			// If the user is muted, queue the message to send later
			log.Printf("User is muted. Queuing message: %s", hand)
			// ToDo:
			// messageQueue = append(messageQueue, hand)
		}
	} else {
		// Log the current sum for debugging purposes
		log.Printf("Evaluating hand: Current sum = %d\n", currentSum)

		// If sum is less than 15, call hitBjDice to roll another dice
		if currentSum < 7 {
			log.Println("Sum is less than 7. Hitting another dice.")
			a.hit13Dice() // This will hit the dice and then re-evaluate the hand
			return        // Return early after hitting, so we don't send a message yet
		}

		// Convert sum to string and send to chat
		hand := strconv.Itoa(currentSum)
		logRollResult := fmt.Sprintf("13 Result: %s\n", hand)
		time.Sleep(time.Duration(rand.Intn(250)+250) * time.Millisecond)
		a.AddLogMsg(logRollResult)
	}
	is13Rolling = false
	is13Hitting = false
}

// Wait for all dice results and evaluate the tri hand
func (a *App) evaluateTriHand() {
	if !ChatIsDisabled {
		hand := sumHand([]int{
			diceList[0].Value,
			diceList[2].Value,
			diceList[4].Value,
		})
		logRollResult := fmt.Sprintf("Tri Result: %s\n", hand)
		time.Sleep(time.Duration(rand.Intn(250)+250) * time.Millisecond)
		a.AddLogMsg(logRollResult)

		if !isMuted {
			// If the user is not muted, send the message
			sendMessageWithDelay(hand)
		} else {
			// If the user is muted, queue the message to send later
			log.Printf("User is muted. Queuing message: %s", hand)
			// ToDo:
			// messageQueue = append(messageQueue, hand)
		}
	} else {
		hand := sumHand([]int{
			diceList[0].Value,
			diceList[2].Value,
			diceList[4].Value,
		})
		logRollResult := fmt.Sprintf("Tri Result: %s\n", hand)
		time.Sleep(time.Duration(rand.Intn(250)+250) * time.Millisecond)
		a.AddLogMsg(logRollResult)
	}
	isTriRolling = false
}

func (a *App) logAndMaybeShout(logMessage string, chatMessage string) {
	time.Sleep(time.Duration(rand.Intn(250)+250) * time.Millisecond)
	a.AddLogMsg(fmt.Sprintf("%s\n", logMessage))
	if ChatIsDisabled {
		return
	}
	if isMuted {
		log.Printf("User is muted. Queuing message: %s", chatMessage)
		return
	}
	sendMessageWithDelay(chatMessage)
}

// Sum the values of the dice and return a string representation
func sumHand(values []int) string {
	sum := 0
	for _, val := range values {
		sum += val
	}
	return strconv.Itoa(sum)
}

// Sum the values of the dice and return the integer sum
func sumHandInt(values []int) int {
	sum := 0
	for _, val := range values {
		sum += val
	}
	return sum
}

// Evaluate the hand of dice and return a string representation
// thank you b7 <3 (and me, eduard, selfplug lol)
func (a *App) toPokerString(dices []*Dice) string {
	hand := a.toPokerHandResult(dices)
	return hand.Description
}

type PokerHandResult struct {
	Rank        int
	Description string
	Tiebreakers []int
	DiceValues  []int
}

func (p PokerHandResult) DiceString() string {
	parts := make([]string, 0, len(p.DiceValues))
	for _, value := range p.DiceValues {
		parts = append(parts, strconv.Itoa(value))
	}
	return fmt.Sprintf("[%s]", strings.Join(parts, ","))
}

func (a *App) toPokerHandResult(dices []*Dice) PokerHandResult {
	// Load user configuration
	config := a.LoadConfig()

	if config != nil {
		// Use the loaded config
		// fmt.Println("Config loaded:", config)
	} else {
		// Use default values if no config is found
		fmt.Println("Using default configuration")
		config = &PokerDisplayConfig{
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

	diceValues := make([]int, 0, len(dices))
	for _, dice := range dices {
		diceValues = append(diceValues, dice.Value)
	}
	s := ""
	for _, value := range diceValues {
		s += strconv.Itoa(value)
	}
	runes := []rune(s)
	sort.Slice(runes, func(i, j int) bool {
		return runes[i] < runes[j]
	})
	s = string(runes)

	if s == "12345" {
		return PokerHandResult{
			Rank:        4,
			Description: fmt.Sprintf(config.LowStraight),
			Tiebreakers: []int{5},
			DiceValues:  diceValues,
		}
	}
	if s == "23456" {
		return PokerHandResult{
			Rank:        4,
			Description: fmt.Sprintf(config.HighStraight),
			Tiebreakers: []int{6},
			DiceValues:  diceValues,
		}
	}

	mapCount := make(map[int]int)
	for _, c := range s {
		mapCount[int(c-'0')]++
	}

	keys := []int{}
	countValues := []int{}
	for k, v := range mapCount {
		if v > 1 {
			keys = append(keys, k)
			countValues = append(countValues, v)
		}
	}

	if len(keys) == 0 {
		tiebreakers := append([]int{}, diceValues...)
		sort.Slice(tiebreakers, func(i, j int) bool { return tiebreakers[i] > tiebreakers[j] })
		return PokerHandResult{
			Rank:        0,
			Description: fmt.Sprintf(config.Nothing),
			Tiebreakers: tiebreakers,
			DiceValues:  diceValues,
		}
	}

	sort.Slice(keys, func(i, j int) bool { return keys[i] > keys[j] })
	sort.Slice(countValues, func(i, j int) bool { return countValues[i] > countValues[j] })

	n := strings.Trim(strings.Replace(fmt.Sprint(keys), " ", "", -1), "[]")
	c := strings.Trim(strings.Replace(fmt.Sprint(countValues), " ", "", -1), "[]")

	switch c {
	case "5":
		return PokerHandResult{
			Rank:        7,
			Description: fmt.Sprintf(config.FiveOfAKind, formatKindSuffix(keys[0])),
			Tiebreakers: []int{keys[0]},
			DiceValues:  diceValues,
		}
	case "4":
		return PokerHandResult{
			Rank:        6,
			Description: fmt.Sprintf(config.FourOfAKind, formatKindSuffix(keys[0])),
			Tiebreakers: []int{keys[0]},
			DiceValues:  diceValues,
		}
	case "3":
		kickers := kickersForCount(mapCount, 3)
		return PokerHandResult{
			Rank:        3,
			Description: fmt.Sprintf(config.ThreeOfAKind, formatKindSuffix(keys[0])),
			Tiebreakers: append([]int{keys[0]}, kickers...),
			DiceValues:  diceValues,
		}
	case "32":
		var threeOfAKind, pair int

		// Loop through the map to find the three-of-a-kind and the pair
		for num, count := range mapCount {
			if count == 3 {
				threeOfAKind = num
			} else if count == 2 {
				pair = num
			}
		}

		// Construct the string with the three-of-a-kind first
		n = formatKindSuffix(threeOfAKind) + formatKindSuffix(pair)
		return PokerHandResult{
			Rank:        5,
			Description: fmt.Sprintf(config.FullHouse, n),
			Tiebreakers: []int{threeOfAKind, pair},
			DiceValues:  diceValues,
		}
	case "22":
		pairs := orderedPairs(mapCount)
		n = formatKindSuffix(pairs[0]) + formatKindSuffix(pairs[1])
		kicker := kickerForPairs(mapCount, pairs)
		return PokerHandResult{
			Rank:        2,
			Description: fmt.Sprintf(config.TwoPair, n),
			Tiebreakers: []int{pairs[0], pairs[1], kicker},
			DiceValues:  diceValues,
		}
	case "2":
		pairValue := keys[0]
		kickers := kickersForCount(mapCount, 2)
		return PokerHandResult{
			Rank:        1,
			Description: fmt.Sprintf(config.OnePair, formatKindSuffix(pairValue)),
			Tiebreakers: append([]int{pairValue}, kickers...),
			DiceValues:  diceValues,
		}
	default:
		tiebreakers := append([]int{}, diceValues...)
		sort.Slice(tiebreakers, func(i, j int) bool { return tiebreakers[i] > tiebreakers[j] })
		return PokerHandResult{
			Rank:        0,
			Description: n + "",
			Tiebreakers: tiebreakers,
			DiceValues:  diceValues,
		}
	}
}

func formatKindSuffix(value int) string {
	return fmt.Sprintf("%ds", value)
}

func orderedPairs(counts map[int]int) []int {
	pairs := make([]int, 0, 2)
	for value, count := range counts {
		if count == 2 {
			pairs = append(pairs, value)
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i] > pairs[j] })
	return pairs
}

func kickerForPairs(counts map[int]int, pairs []int) int {
	for value, count := range counts {
		if count == 1 {
			return value
		}
	}
	return 0
}

func kickersForCount(counts map[int]int, target int) []int {
	kickers := make([]int, 0)
	for value, count := range counts {
		if count == 1 {
			kickers = append(kickers, value)
		}
	}
	sort.Slice(kickers, func(i, j int) bool { return kickers[i] > kickers[j] })
	return kickers
}

func comparePokerHands(player PokerHandResult, dealer PokerHandResult) string {
	if player.Rank != dealer.Rank {
		if player.Rank > dealer.Rank {
			return fmt.Sprintf("%s beats %s, Player wins.", rankName(player.Rank), rankName(dealer.Rank))
		}
		return fmt.Sprintf("%s beats %s, Dealer wins.", rankName(dealer.Rank), rankName(player.Rank))
	}

	for i := 0; i < len(player.Tiebreakers) && i < len(dealer.Tiebreakers); i++ {
		if player.Tiebreakers[i] == dealer.Tiebreakers[i] {
			continue
		}
		if player.Tiebreakers[i] > dealer.Tiebreakers[i] {
			return fmt.Sprintf("%s beats %s, Player wins.", rankName(player.Rank), rankName(dealer.Rank))
		}
		return fmt.Sprintf("%s beats %s, Dealer wins.", rankName(dealer.Rank), rankName(player.Rank))
	}

	return "Tie game."
}

func rankName(rank int) string {
	switch rank {
	case 7:
		return "Five of a Kind"
	case 6:
		return "Four of a Kind"
	case 5:
		return "Full House"
	case 4:
		return "Straight"
	case 3:
		return "Three of a Kind"
	case 2:
		return "Two Pair"
	case 1:
		return "Pair"
	default:
		return "High Card"
	}
}
