package main

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation -framework Carbon
#include <ApplicationServices/ApplicationServices.h>
#include <Carbon/Carbon.h>
#include <pthread.h>
#include <unistd.h>
#include <stdio.h>

// Write end of the pipe — only touched by the C callback
static int g_write_fd = -1;

// Paste via Cmd+V into the focused window
static void paste() {
    CGEventRef cmdDown = CGEventCreateKeyboardEvent(NULL, kVK_Command, true);
    CGEventRef vDown   = CGEventCreateKeyboardEvent(NULL, kVK_ANSI_V, true);
    CGEventRef vUp     = CGEventCreateKeyboardEvent(NULL, kVK_ANSI_V, false);
    CGEventRef cmdUp   = CGEventCreateKeyboardEvent(NULL, kVK_Command, false);
    CGEventSetFlags(vDown, kCGEventFlagMaskCommand);
    CGEventSetFlags(vUp,   kCGEventFlagMaskCommand);
    CGEventPost(kCGAnnotatedSessionEventTap, cmdDown);
    CGEventPost(kCGAnnotatedSessionEventTap, vDown);
    CGEventPost(kCGAnnotatedSessionEventTap, vUp);
    CGEventPost(kCGAnnotatedSessionEventTap, cmdUp);
    CFRelease(cmdDown); CFRelease(vDown); CFRelease(vUp); CFRelease(cmdUp);
}

// Push-to-talk keycode — default F13 (105), override via WHISPER_KEYCODE env var.
// Uses kCGEventKeyDown/Up (not modifier flags) so it works on macOS 16+.
static int g_ptt_keycode = 109; // F10 (audio key on Mac)

void setPTTKeycode(int kc) { g_ptt_keycode = kc; }

static CGEventRef tapCallback(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *refcon) {
    int keycode = (int)CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode);
    if (keycode != g_ptt_keycode) return event;
    if (type == kCGEventKeyDown) {
        int64_t repeat = CGEventGetIntegerValueField(event, kCGKeyboardEventAutorepeat);
        if (!repeat) write(g_write_fd, "D", 1); // first press only, not auto-repeat
    } else if (type == kCGEventKeyUp) {
        write(g_write_fd, "U", 1);
    }
    return event;
}

// Runs entirely on a C-owned pthread — CFRunLoop lives here, Go never touches it
static void* eventThread(void *arg) {
    CGEventMask mask = CGEventMaskBit(kCGEventKeyDown) | CGEventMaskBit(kCGEventKeyUp);
    CFMachPortRef tap = CGEventTapCreate(
        kCGSessionEventTap,
        kCGHeadInsertEventTap,
        kCGEventTapOptionListenOnly,
        mask,
        tapCallback,
        NULL
    );
    if (!tap) {
        char msg = 'E';
        write(g_write_fd, &msg, 1);
        return NULL;
    }
    CFRunLoopSourceRef src = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, tap, 0);
    CFRunLoopAddSource(CFRunLoopGetCurrent(), src, kCFRunLoopCommonModes);
    CGEventTapEnable(tap, true);
    CFRunLoopRun(); // owns this thread forever
    return NULL;
}

// Called once from Go main — returns the read end of the pipe
static int startMonitoring() {
    int fds[2];
    if (pipe(fds) < 0) return -1;
    g_write_fd = fds[1];
    pthread_t t;
    pthread_create(&t, NULL, eventThread, NULL);
    pthread_detach(t);
    return fds[0];
}
*/
import "C"

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gordonklaus/portaudio"
)

// whisperAnnotation matches Whisper's non-speech output: (music), (phone buzzing), ♪ etc.
var whisperAnnotation = regexp.MustCompile(`^[\s\(\[♪\*]*[^a-zA-Z]*[\)\]♪\*]*$|^\s*\([^)]*\)\s*$`)

const framesPerBuf = 1024

var (
	mu          sync.Mutex
	samples     []float32
	actualRate  int
	stream      *portaudio.Stream
	recordStart time.Time
)

func onKeyDown() {
	mu.Lock()
	defer mu.Unlock()
	samples = samples[:0]
	recordStart = time.Now()

	// Use the device's native sample rate to avoid PortAudio resampling artifacts
	dev, err := portaudio.DefaultInputDevice()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] No input device: %v\n", err)
		return
	}
	rate := int(dev.DefaultSampleRate)
	actualRate = rate

	buf := make([]float32, framesPerBuf)
	stream, err = portaudio.OpenDefaultStream(1, 0, float64(rate), framesPerBuf, buf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Cannot open mic: %v\n", err)
		return
	}
	if err := stream.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Cannot start mic: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "[REC] Recording at %dHz... (release F10 to stop)\n", rate)

	go func() {
		for {
			mu.Lock()
			s := stream
			mu.Unlock()
			if s == nil {
				return
			}
			if err := s.Read(); err != nil {
				return
			}
			mu.Lock()
			samples = append(samples, buf...)
			mu.Unlock()
		}
	}()
}

func onKeyUp() {
	mu.Lock()
	s := stream
	captured := make([]float32, len(samples))
	copy(captured, samples)
	dur := time.Since(recordStart)
	stream = nil
	mu.Unlock()

	if s != nil {
		s.Stop()
		s.Close()
	}
	if len(captured) < actualRate/4 {
		fmt.Fprintln(os.Stderr, "[SKIP] Too short, ignored.")
		return
	}
	fmt.Fprintf(os.Stderr, "[REC] Captured %.1fs — transcribing...\n", dur.Seconds())
	go transcribeAndPaste(captured)
}

func transcribeAndPaste(pcm []float32) {
	tmp, err := os.CreateTemp("", "whisper-*.wav")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		return
	}
	defer os.Remove(tmp.Name())
	writeWAV(tmp, pcm)
	tmp.Close()

	home, _ := os.UserHomeDir()
	model := filepath.Join(home, ".cache", "localvoice", "ggml-small.bin")
	out, err := exec.Command("whisper-cli",
		"-m", model,
		"-f", tmp.Name(),
		"--no-timestamps",
		"-otxt",
		"--output-file", tmp.Name(),
		"--no-speech-thold", "0.8", // raise threshold to reduce noise/music false positives
	).CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] whisper-cli: %v\n%s\n", err, string(out))
		return
	}

	txtPath := tmp.Name() + ".txt"
	defer os.Remove(txtPath)
	data, err := os.ReadFile(txtPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] reading result: %v\n", err)
		return
	}

	text := strings.TrimSpace(string(data))
	if text == "" {
		fmt.Fprintln(os.Stderr, "[SKIP] Nothing detected.")
		return
	}
	if whisperAnnotation.MatchString(text) {
		fmt.Fprintf(os.Stderr, "[SKIP] Non-speech audio ignored: %s\n", text)
		return
	}

	pb := exec.Command("pbcopy")
	pb.Stdin = strings.NewReader(text)
	if err := pb.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] pbcopy: %v\n", err)
		return
	}

	C.paste()
	fmt.Fprintf(os.Stderr, "[OK]  Pasted: %s\n", text)
}

func writeWAV(f *os.File, pcm []float32) {
	mu.Lock()
	rate := uint32(actualRate)
	mu.Unlock()
	n := len(pcm)
	dataSize := n * 2
	writeLE32(f, 0x46464952)
	writeLE32(f, uint32(36+dataSize))
	writeLE32(f, 0x45564157)
	writeLE32(f, 0x20746d66)
	writeLE32(f, 16)
	writeLE16(f, 1)
	writeLE16(f, 1)
	writeLE32(f, rate)
	writeLE32(f, rate*2)
	writeLE16(f, 2)
	writeLE16(f, 16)
	writeLE32(f, 0x61746164)
	writeLE32(f, uint32(dataSize))
	for _, s := range pcm {
		v := int16(s * math.MaxInt16)
		f.Write([]byte{byte(v), byte(v >> 8)})
	}
}

func writeLE32(f *os.File, v uint32) {
	f.Write([]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)})
}
func writeLE16(f *os.File, v uint16) {
	f.Write([]byte{byte(v), byte(v >> 8)})
}

func main() {
	home, _ := os.UserHomeDir()
	model := filepath.Join(home, ".cache", "localvoice", "ggml-small.bin")

	if _, err := os.Stat(model); err != nil {
		if err := downloadModel(model); err != nil {
			fmt.Fprintln(os.Stderr, "[ERROR] Could not download model:", err)
			os.Exit(1)
		}
	}
	if _, err := exec.LookPath("whisper-cli"); err != nil {
		fmt.Fprintln(os.Stderr, "[ERROR] whisper-cli not found. Run: brew install whisper-cpp")
		os.Exit(1)
	}
	if err := portaudio.Initialize(); err != nil {
		fmt.Fprintln(os.Stderr, "[ERROR] PortAudio init failed:", err)
		os.Exit(1)
	}
	defer portaudio.Terminate()

	// Keycode config: default F10 (109), override with WHISPER_KEYCODE=<decimal>
	keycode := int64(109)
	if kcs := os.Getenv("WHISPER_KEYCODE"); kcs != "" {
		if n, err := fmt.Sscanf(kcs, "%d", &keycode); n != 1 || err != nil {
			fmt.Fprintln(os.Stderr, "[WARN] Invalid WHISPER_KEYCODE, using 109 (F10)")
			keycode = 109
		}
	}
	C.setPTTKeycode(C.int(keycode))

	keyName := "F10 (keycode 109)"
	if keycode != 109 {
		keyName = fmt.Sprintf("keycode %d", keycode)
	}

	fmt.Fprintln(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Fprintln(os.Stderr, "  LocalVoiceAI  (local GPU STT)")
	fmt.Fprintln(os.Stderr, "  Model:", filepath.Base(model))
	fmt.Fprintln(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Fprintf(os.Stderr, "  Hold %s to record\n", keyName)
	fmt.Fprintln(os.Stderr, "  Release to transcribe + paste")
	fmt.Fprintln(os.Stderr, "  Override: WHISPER_KEYCODE=<decimal keycode>")
	fmt.Fprintln(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	readFd := int(C.startMonitoring())
	if readFd < 0 {
		fmt.Fprintln(os.Stderr, "[ERROR] pipe() failed")
		os.Exit(1)
	}

	fmt.Fprintln(os.Stderr, "[OK]  Listening... (Accessibility + Input Monitoring permissions required)")

	// Read key events from the C-owned pthread via pipe
	// 'D' = Option down, 'U' = Option up, 'E' = tap creation failed
	buf := make([]byte, 1)
	optionDown := false
	for {
		n, err := syscall.Read(readFd, buf)
		if err != nil || n == 0 {
			continue
		}
		switch buf[0] {
		case 'E':
			fmt.Fprintln(os.Stderr, "[ERROR] Event tap failed — missing permissions.")
			fmt.Fprintln(os.Stderr, "  1. System Settings → Privacy & Security → Accessibility → add localvoice")
			fmt.Fprintln(os.Stderr, "  2. System Settings → Privacy & Security → Input Monitoring → add localvoice")
			fmt.Fprintln(os.Stderr, "  Note: re-grant both permissions after every rebuild (binary hash changes)")
			os.Exit(1)
		case 'D':
			if !optionDown {
				optionDown = true
				onKeyDown()
			}
		case 'U':
			if optionDown {
				optionDown = false
				onKeyUp()
			}
		}
	}
}

func downloadModel(dest string) error {
	const url = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin"
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	fmt.Fprintln(os.Stderr, "[SETUP] Downloading Whisper model (~244MB) on first run...")
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	total := resp.ContentLength
	var written int64
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				os.Remove(tmp)
				return fmt.Errorf("write: %w", werr)
			}
			written += int64(n)
			if total > 0 {
				fmt.Fprintf(os.Stderr, "\r[SETUP] %.1f%%", float64(written)/float64(total)*100)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("read: %w", err)
		}
	}
	f.Close()
	fmt.Fprintln(os.Stderr, "\n[SETUP] Model saved:", dest)
	return os.Rename(tmp, dest)
}
