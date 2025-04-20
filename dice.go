package main

import (
	"encoding/binary"
	"errors"
	"fmt"

	legacyOut "xabbo.b7c.io/goearth/out"
	"xabbo.b7c.io/goearth/shockwave/out"
)

// Dice struct represents a dice with its ID, value
type Dice struct {
	ID        int
	Value     int
	IsClosed  bool
	IsRolling bool
}

// Roll the dice
func (d *Dice) Roll() error {
	if d.ID == 0 {
		return errors.New("no dice id")
	}

	// Check if using Flash (Legacy) or Shockwave (Origins)
	if isFlash != nil && *isFlash {
		// Flash (Legacy) uses raw binary data (4 bytes for the dice ID)
		diceIDBytes := make([]byte, 4)
		// Convert diceID to a 4-byte slice (BigEndian) and send it
		binary.BigEndian.PutUint32(diceIDBytes, uint32(d.ID))
		ext.Send(legacyOut.ThrowDice, diceIDBytes)
	} else {
		// Shockwave (Origins) sends the dice ID as a string (or simple number)
		// Send the throw dice packet
		ext.Send(out.THROW_DICE, []byte(fmt.Sprintf("%d", d.ID)))
	}
	d.IsRolling = true
	d.IsClosed = false
	return nil
}

// Close the dice
func (d *Dice) Close() error {
	if d.ID == 0 {
		return errors.New("no dice id")
	}

	// Check if using Flash (Legacy) or Shockwave (Origins)
	if isFlash != nil && *isFlash {
		// Flash (Legacy) uses raw binary data (4 bytes for the dice ID)
		diceIDBytes := make([]byte, 4)
		// Convert diceID to a 4-byte slice (BigEndian) and send it
		binary.BigEndian.PutUint32(diceIDBytes, uint32(d.ID))
		ext.Send(legacyOut.DiceOff, diceIDBytes)
	} else {
		// Send the dice off packet
		ext.Send(out.DICE_OFF, []byte(fmt.Sprintf("%d", d.ID)))
	}
	d.IsClosed = true
	return nil
}

// IsValid checks if the dice is valid
func (d *Dice) IsValid() bool {
	return d.ID != 0
}
