package parser_test

import (
	"strings"
	"testing"

	"docksmith/parser"
)

func TestParseContentValidDocksmithfile(t *testing.T) {
	content := `
FROM alpine:latest
WORKDIR /app
COPY hello.sh /app/
RUN chmod +x /app/hello.sh
ENV PORT=8080
CMD ["/app/hello.sh"]
`
	instructions, err := parser.ParseContent(content)
	if err != nil {
		t.Fatalf("ParseContent failed: %v", err)
	}
	if len(instructions) != 6 {
		t.Fatalf("expected 6 instructions, got %d", len(instructions))
	}
	if instructions[0].Type != parser.InstrFROM {
		t.Fatalf("first instruction should be FROM")
	}
	if instructions[0].FromImage != "alpine" || instructions[0].FromTag != "latest" {
		t.Fatalf("unexpected FROM: %s:%s", instructions[0].FromImage, instructions[0].FromTag)
	}
	if instructions[1].Type != parser.InstrWORKDIR || instructions[1].Args != "/app" {
		t.Fatalf("unexpected WORKDIR: %v", instructions[1])
	}
	if instructions[2].Type != parser.InstrCOPY {
		if instructions[2].CopySrc != "hello.sh" || instructions[2].CopyDst != "/app/" {
			t.Fatalf("unexpected COPY: %s -> %s", instructions[2].CopySrc, instructions[2].CopyDst)
		}
	}
	if instructions[3].Type != parser.InstrRUN {
		t.Fatalf("expected RUN")
	}
	if instructions[4].Type != parser.InstrENV {
		if instructions[4].EnvKey != "PORT" || instructions[4].EnvValue != "8080" {
			t.Fatalf("unexpected ENV: %s=%s", instructions[4].EnvKey, instructions[4].EnvValue)
		}
	}
	if instructions[5].Type != parser.InstrCMD {
		if len(instructions[5].CmdArgs) != 1 || instructions[5].CmdArgs[0] != "/app/hello.sh" {
			t.Fatalf("unexpected CMD: %v", instructions[5].CmdArgs)
		}
	}
}

func TestParseContentRequiresFROMFirst(t *testing.T) {
	_, err := parser.ParseContent("WORKDIR /app\nFROM alpine:latest")
	if err == nil {
		t.Fatal("expected error when FROM is not first")
	}
}

func TestParseContentRejectsInvalidCMD(t *testing.T) {
	_, err := parser.ParseContent("FROM alpine:latest\nCMD /bin/sh")
	if err == nil {
		t.Fatal("expected error for non-JSON CMD")
	}
}

func TestParseContentSkipsCommentsAndBlankLines(t *testing.T) {
	content := `
# comment
FROM alpine:latest

# another comment
ENV FOO=bar
`
	instructions, err := parser.ParseContent(content)
	if err != nil {
		t.Fatalf("ParseContent failed: %v", err)
	}
	if len(instructions) != 2 {
		t.Fatalf("expected 2 instructions, got %d", len(instructions))
	}
}

func TestParseContentRejectsUnknownInstruction(t *testing.T) {
	_, err := parser.ParseContent("FROM alpine:latest\nEXPOSE 80")
	if err == nil || !strings.Contains(err.Error(), "unrecognised") {
		t.Fatalf("expected unrecognised instruction error, got %v", err)
	}
}

func TestParseContentCOPYRequiresTwoArgs(t *testing.T) {
	_, err := parser.ParseContent("FROM alpine:latest\nCOPY only-one-arg")
	if err == nil {
		t.Fatal("expected COPY arity error")
	}
}

func TestParseContentENVRequiresEquals(t *testing.T) {
	_, err := parser.ParseContent("FROM alpine:latest\nENV MISSING_EQUALS")
	if err == nil {
		t.Fatal("expected ENV format error")
	}
}
