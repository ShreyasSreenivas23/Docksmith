package parser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InstructionType represents the type of a Docksmithfile instruction.
type InstructionType int

const (
	InstrFROM InstructionType = iota
	InstrCOPY
	InstrRUN
	InstrWORKDIR
	InstrENV
	InstrCMD
)

// String returns the instruction type name.
func (t InstructionType) String() string {
	switch t {
	case InstrFROM:
		return "FROM"
	case InstrCOPY:
		return "COPY"
	case InstrRUN:
		return "RUN"
	case InstrWORKDIR:
		return "WORKDIR"
	case InstrENV:
		return "ENV"
	case InstrCMD:
		return "CMD"
	default:
		return "UNKNOWN"
	}
}

// Instruction represents a parsed Docksmithfile instruction.
type Instruction struct {
	Type     InstructionType
	Args     string
	Line     int
	FullText string
	CmdArgs  []string
	CopySrc  string
	CopyDst  string
	EnvKey   string
	EnvValue string
	FromImage string
	FromTag   string
}

// Parse reads and parses a Docksmithfile from the given context directory.
func Parse(contextDir string) ([]Instruction, error) {
	docksmithfilePath := filepath.Join(contextDir, "Docksmithfile")
	data, err := os.ReadFile(docksmithfilePath)
	if err != nil {
		return nil, fmt.Errorf("cannot read Docksmithfile: %w", err)
	}
	return ParseContent(string(data))
}

// ParseContent parses the content string of a Docksmithfile.
func ParseContent(content string) ([]Instruction, error) {
	var instructions []Instruction
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		lineNum := i + 1
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		instr, err := parseLine(line, lineNum)
		if err != nil {
			return nil, err
		}
		instructions = append(instructions, instr)
	}

	if len(instructions) == 0 {
		return nil, fmt.Errorf("Docksmithfile is empty")
	}
	if instructions[0].Type != InstrFROM {
		return nil, fmt.Errorf("line %d: first instruction must be FROM", instructions[0].Line)
	}
	return instructions, nil
}

func parseLine(line string, lineNum int) (Instruction, error) {
	parts := strings.SplitN(line, " ", 2)
	keyword := strings.ToUpper(parts[0])
	args := ""
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}

	instr := Instruction{
		Args:     args,
		Line:     lineNum,
		FullText: line,
	}

	switch keyword {
	case "FROM":
		instr.Type = InstrFROM
		if args == "" {
			return instr, fmt.Errorf("line %d: FROM requires an image argument", lineNum)
		}
		imgParts := strings.SplitN(args, ":", 2)
		instr.FromImage = imgParts[0]
		if len(imgParts) == 2 {
			instr.FromTag = imgParts[1]
		} else {
			instr.FromTag = "latest"
		}

	case "COPY":
		instr.Type = InstrCOPY
		copyParts := strings.Fields(args)
		if len(copyParts) != 2 {
			return instr, fmt.Errorf("line %d: COPY requires exactly two arguments (src dest)", lineNum)
		}
		instr.CopySrc = copyParts[0]
		instr.CopyDst = copyParts[1]

	case "RUN":
		instr.Type = InstrRUN
		if args == "" {
			return instr, fmt.Errorf("line %d: RUN requires a command", lineNum)
		}

	case "WORKDIR":
		instr.Type = InstrWORKDIR
		if args == "" {
			return instr, fmt.Errorf("line %d: WORKDIR requires a path", lineNum)
		}

	case "ENV":
		instr.Type = InstrENV
		eqIdx := strings.Index(args, "=")
		if eqIdx < 0 {
			return instr, fmt.Errorf("line %d: ENV requires KEY=VALUE format", lineNum)
		}
		instr.EnvKey = strings.TrimSpace(args[:eqIdx])
		instr.EnvValue = strings.TrimSpace(args[eqIdx+1:])
		if instr.EnvKey == "" {
			return instr, fmt.Errorf("line %d: ENV key cannot be empty", lineNum)
		}

	case "CMD":
		instr.Type = InstrCMD
		if args == "" {
			return instr, fmt.Errorf("line %d: CMD requires arguments", lineNum)
		}
		var cmdArgs []string
		if err := json.Unmarshal([]byte(args), &cmdArgs); err != nil {
			return instr, fmt.Errorf("line %d: CMD must be a JSON array (e.g. [\"exec\",\"arg\"]): %w", lineNum, err)
		}
		if len(cmdArgs) == 0 {
			return instr, fmt.Errorf("line %d: CMD array cannot be empty", lineNum)
		}
		instr.CmdArgs = cmdArgs

	default:
		return instr, fmt.Errorf("line %d: unrecognised instruction %q", lineNum, keyword)
	}

	return instr, nil
}
