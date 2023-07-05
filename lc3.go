package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// Memory
const MemoryMax = 1 << 16

var memory [MemoryMax]uint16 // 65536 locations

const (
	MR_KBSR = 0xFE00 /* keyboard status */
	MR_KBDR = 0xFE02 /* keyboard data */
)

// Registers
const (
	R_R0 = iota //incremental value to const values, starts from 0
	R_R1
	R_R2
	R_R3
	R_R4
	R_R5
	R_R6
	R_R7
	R_PC // program counter
	R_COND
	R_COUNT
)

var reg [R_COUNT]uint16

// Instructions
const (
	OP_BR   = iota // branch
	OP_ADD         // add
	OP_LD          // load
	OP_ST          // store
	OP_JSR         // jump register
	OP_AND         // bitwise and
	OP_LDR         // load register
	OP_STR         // store register
	OP_RTI         // unused
	OP_NOT         // bitwise not
	OP_LDI         // load indirect
	OP_STI         // store indirect
	OP_JMP         // jump
	OP_RES         // reserved (unused)
	OP_LEA         // load effective address
	OP_TRAP        // execute trap
)

// Condition flags
const (
	FL_POS = 1 << 0 // P
	FL_ZRO = 1 << 1 // Z
	FL_NEG = 1 << 2 // N
)

// Trap codes
const (
	TRAP_GETC  uint16 = 0x20 // get character from keyboard, not echoed onto the terminal
	TRAP_OUT   uint16 = 0x21 // output a character
	TRAP_PUTS  uint16 = 0x22 // output a word string
	TRAP_IN    uint16 = 0x23 // get character from keyboard, echoed onto the terminal
	TRAP_PUTSP uint16 = 0x24 // output a byte string
	TRAP_HALT  uint16 = 0x25 // halt the program
)

var running bool = false

func handleInterrupt() {
	// Handle SIGINT signal
	fmt.Println("Received SIGINT signal. Handling interrupt...")
	// Add your interrupt handling code here
	os.Exit(0)
}

func signExtend(x uint16, bitCount int) uint16 {
	// Puts 1 if negative or 0 if positive
	if (x>>(bitCount-1))&1 == 1 {
		x |= (0xFFFF << bitCount)
	}
	return x
}

func updateFlags(r uint16) {
	if reg[r] == 0 {
		reg[R_COND] = FL_ZRO
	} else if reg[r]>>15 == 1 { // a 1 in the left-most bit indicates negative
		reg[R_COND] = FL_NEG
	} else {
		reg[R_COND] = FL_POS
	}
}

func add(instr uint16) {
	// Destination register (DR)
	r0 := (instr >> 9) & 0x7
	// First operand (SR1)
	r1 := (instr >> 6) & 0x7
	// Whether we are in immediate mode
	immFlag := (instr >> 5) & 0x1

	if immFlag == 1 {
		imm5 := signExtend(instr&0x1F, 5)
		reg[r0] = reg[r1] + imm5
	} else {
		r2 := instr & 0x7
		reg[r0] = reg[r1] + reg[r2]
	}

	updateFlags(r0)
}

func and(instr uint16) {
	// Destination register (DR)
	r0 := (instr >> 9) & 0x7
	// First operand (SR1)
	r1 := (instr >> 6) & 0x7
	// Whether we are in immediate mode
	immFlag := (instr >> 5) & 0x1

	if immFlag == 1 {
		imm5 := signExtend(instr&0x1F, 5)
		reg[r0] = reg[r1] & imm5
	} else {
		r2 := instr & 0x7
		reg[r0] = reg[r1] & reg[r2]
	}

	updateFlags(r0)
}

func not(instr uint16) {
	// Destination register (DR)
	r0 := (instr >> 9) & 0x7
	// First operand (SR1)
	r1 := (instr >> 6) & 0x7

	reg[r0] = ^reg[r1]
	updateFlags(r0)
}

func br(instr uint16) {
	// PCoffset 9
	pcOffset := signExtend(instr&0x1FF, 9)
	// Condition flag
	condFlag := (instr >> 9) & 0x7

	if condFlag&reg[R_COND] != 0 {
		reg[R_PC] += pcOffset
	}
}

func jmp(instr uint16) {

	/*
		Also handles RET
		RET is listed as a separate instruction in the specification,
		since it is a different keyword in assembly.
		However, it is actually a special case of JMP.
		RET happens whenever R1 is 7.
	*/

	// First operand (SR1)
	r1 := (instr >> 6) & 0x7
	reg[R_PC] = reg[r1]
}

func jsr(instr uint16) {
	// Long flag
	longFlag := (instr >> 11) & 1
	reg[R_R7] = reg[R_PC]

	if longFlag == 1 {
		longPcOffset := signExtend(instr&0x7FF, 11)
		reg[R_PC] += longPcOffset // JSR
	} else {
		r1 := (instr >> 6) & 0x7
		reg[R_PC] = reg[r1] // JSRR
	}
}

func ld(instr uint16) {
	// Destination register (DR)
	r0 := (instr >> 9) & 0x7
	// PCoffset 9
	pcOffset := signExtend(instr&0x1FF, 9)

	reg[r0] = memRead(reg[R_PC] + pcOffset)
	updateFlags(r0)
}

func ldi(instr uint16) {
	// destination register (DR)
	r0 := (instr >> 9) & 0x7

	// PC offset 9
	pcOffset := signExtend(instr&0x1FF, 9)

	//add pcOffset to current memory position and gets val of the stored pointer
	reg[r0] = memRead(memRead(reg[R_PC] + pcOffset))
	updateFlags(r0)
}

func ldr(instr uint16) {
	// Destination register (DR)
	r0 := (instr >> 9) & 0x7
	// Base register (SR)
	r1 := (instr >> 6) & 0x7
	// Offset 6
	offset := signExtend(instr&0x3F, 6)

	reg[r0] = memRead(reg[r1] + offset)
	updateFlags(r0)
}

func lea(instr uint16) {
	// Destination register (DR)
	r0 := (instr >> 9) & 0x7
	// PCoffset 9
	pcOffset := signExtend(instr&0x1FF, 9)

	reg[r0] = reg[R_PC] + pcOffset
	updateFlags(r0)
}

func st(instr uint16) {
	// Source register (SR)
	r0 := (instr >> 9) & 0x7
	// PCoffset 9
	pcOffset := signExtend(instr&0x1FF, 9)

	memWrite(reg[R_PC]+pcOffset, reg[r0])
}

func sti(instr uint16) {
	// Source register (SR)
	r0 := (instr >> 9) & 0x7
	// PCoffset 9
	pcOffset := signExtend(instr&0x1FF, 9)

	memWrite(memRead(reg[R_PC]+pcOffset), reg[r0])
}

func str(instr uint16) {
	// Destination register (DR)
	r0 := (instr >> 9) & 0x7
	// Base register (SR)
	r1 := (instr >> 6) & 0x7
	// Offset 6
	offset := signExtend(instr&0x3F, 6)

	memWrite(reg[r1]+offset, reg[r0])
}

func getCharFromStdin() uint16 {
	input := bufio.NewReader(os.Stdin)
	char, _, err := input.ReadRune()
	if err != nil {
		panic("Error reading character from stdin")
	}
	return uint16(char)
}

func trapGetc() {
	// Reads a character from stdin and stores on R0
	reg[R_R0] = getCharFromStdin()
	updateFlags(R_R0)
}

func trapOut() {
	// Converts the char in R0 to string to byte buffer and writes on stdout, flushes/syncs right awy
	char := rune(reg[R_R0])
	os.Stdout.Write([]byte(string(char)))
	os.Stdout.Sync()
}

func trapIn() {
	fmt.Print("Enter a character: ")
	char := getCharFromStdin()
	fmt.Printf("%c", char)
	os.Stdout.Sync()
	reg[R_R0] = char
	updateFlags(R_R0)
}

func trapPuts() {
	//Iterate from start memory and stops when we arrive at position where value is 0
	c := memory[reg[R_R0]:]
	for _, value := range c {
		if value == 0 {
			break
		}
		fmt.Printf("%c", value)
	}
	fmt.Println()
}

func trapPutsp() {
	c := memory[reg[R_R0]:]
	for _, value := range c {
		if value == 0 {
			break
		}
		char1 := value & 0xFF
		fmt.Printf("%c", char1)
		char2 := value >> 8
		if char2 != 0 {
			fmt.Printf("%c", char2)
		}
	}
}

func trapHalt() {
	fmt.Printf("HALT")
	running = false
}

func trap(instr uint16) {
	reg[R_R7] = reg[R_PC]

	switch instr & 0xFF {
	case TRAP_GETC:
		trapGetc()
	case TRAP_OUT:
		trapOut()
	case TRAP_PUTS:
		trapPuts()
	case TRAP_IN:
		trapIn()
	case TRAP_PUTSP:
		trapPutsp()
	case TRAP_HALT:
		trapHalt()
	}
}

func abort() {
	panic("Aborted") // Generate a runtime panic
	os.Exit(1)       // This line will not be reached, but included for completeness
}

func memWrite(address uint16, val uint16) {
	memory[address] = val
}

func getCharFromKeyboard() uint16{

}

func memRead(address uint16) uint16 {

	if address == MR_KBSR {
		if check_key() {
			memory[MR_KBSR] = (1 << 15)
			memory[MR_KBDR] = getCharFromKeyboard() //set the 
		} else {
			memory[MR_KBSR] = 0;
		}
	}
	return memory[address]
}

func readImageFile(file []byte) {

	// Probably can be optimized
	origin := len(memory) - len(file)

	j := 0
	for i := origin; i < len(memory); i += 8 {
		memory[i] = swap16(uint16(file[j]))
		++j
	}
}

func swap16(val uint16) uint16 {
	return ((val & 0xFF) << 8) | ((val >> 8) & 0xFF)
}

func readImage(imagePath string) int {
	dat, err := os.ReadFile(imagePath)

	if err != nil {
		panic("Error reading a file")
	}

	readImageFile(dat)

}

func disableIputBuffering() {
	
}

func main() {

	// Load Arguments
	if len(os.Args) < 2 {
		// show usage string
		fmt.Println("lc3 [image-file1] ...")
		os.Exit(2)
	}

	// for j := 1; j < len(os.Args); j++ {
	// 	if !readImage(os.Args[j]) {
	// 		fmt.Printf("failed to load image: %s\n", os.Args[j])
	// 		os.Exit(1)
	// 	}
	// }

	// Setup
	signal.Notify(make(chan os.Signal, 1), syscall.SIGINT)
	go handleInterrupt()

	// disableInputBuffering()

	// since exactly one condition flag should be set at any given time, set the Z flag
	reg[R_COND] = FL_ZRO

	// set the PC to starting position
	const PC_START = 0x3000
	reg[R_PC] = PC_START

	running = true
	for running {
		// FETCH
		instr := memRead(reg[R_PC])
		reg[R_PC]++

		op := instr >> 12 //Look at the opcode

		switch op {
		case OP_ADD:
			add(instr)
			break
		case OP_AND:
			and(instr)
			break
		case OP_NOT:
			not(instr)
			break
		case OP_BR:
			// conditional branch
			br(instr)
			break
		case OP_JMP:
			jmp(instr)
			break
		case OP_JSR:
			jsr(instr)
			break
		case OP_LD:
			ld(instr)
			break
		case OP_LDI:
			ldi(instr)
			break
		case OP_LDR:
			ldr(instr)
			break
		case OP_LEA:
			lea(instr)
			break
		case OP_ST:
			st(instr)
			break
		case OP_STI:
			sti(instr)
			break
		case OP_STR:
			str(instr)
			break
		case OP_TRAP:
			trap(instr)
			break
		case OP_RES:
		case OP_RTI:
		default:
			// BAD OPCODE
			abort()
			break
		}
	}

	// Shutdown
}
