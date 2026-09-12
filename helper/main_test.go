package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestValidationAndMode(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "电影.mp4")
	if err := os.WriteFile(p, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	q := createRequest{SourcePath: p, Title: "电影", DurationSeconds: 10, StartPositionSeconds: 2, Quality: "auto"}
	if err := validateCreate(q); err != nil {
		t.Fatal(err)
	}
	var i mediaInfo
	i.Streams = append(i.Streams, struct {
		Index     int    `json:"index"`
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
	}{0, "video", "h264"})
	i.Streams = append(i.Streams, struct {
		Index     int    `json:"index"`
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
	}{1, "audio", "aac"})
	if got := chooseMode(q, i); got != "direct" {
		t.Fatalf("got %s", got)
	}
	q.Quality = "720p"
	if got := chooseMode(q, i); got != "hardware-transcode" {
		t.Fatalf("got %s", got)
	}
	q.Quality = "auto"
	i.Streams[0].CodecName = "hevc"
	if got := chooseMode(q, i); got != "hardware-transcode" {
		t.Fatalf("HEVC must use browser-safe H.264, got %s", got)
	}
}

func TestTraversalRejected(t *testing.T) {
	s := &server{}
	ss := &session{Generation: 1, TempDir: t.TempDir()}
	r := httptest.NewRequest("GET", "/s/t/hls/1/../../etc/passwd", nil)
	w := httptest.NewRecorder()
	s.hls(w, r, ss, "1/../../etc/passwd")
	if w.Code != 404 {
		t.Fatalf("got %d", w.Code)
	}
}

func TestBundledClient(t *testing.T) {
	const secret = "test-secret"
	var got map[string]any
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-Continue-Secret") != secret {
			t.Fatal("missing secret")
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(`{"ok":true}`))}, nil
	})}
	var output bytes.Buffer
	if err := performClient([]string{"--method", "POST", "--url", "http://127.0.0.1:1234/control/continue", "--secret", secret, "--body", `{"title":"测试"}`}, client, &output); err != nil {
		t.Fatal(err)
	}
	if got["title"] != "测试" {
		t.Fatalf("unexpected payload: %#v", got)
	}
	if output.String() != `{"ok":true}` {
		t.Fatalf("unexpected output: %s", output.String())
	}
}

func TestTokenUses256Bits(t *testing.T) {
	token, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 43 {
		t.Fatalf("expected 43 URL-safe characters, got %d", len(token))
	}
}

func TestMacRequestsLatestPhonePositionBeforeTakingBack(t *testing.T) {
	var output bytes.Buffer
	ss := &session{State: "PHONE_ACTIVE", Position: 42, Duration: 100, Paused: true}
	s := &server{secret: "secret", s: ss, out: json.NewEncoder(&output)}
	r := httptest.NewRequest("POST", "/control/take-back", bytes.NewBufferString(`{}`))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Continue-Secret", "secret")
	w := httptest.NewRecorder()
	s.takeBack(w, r)
	if w.Code != http.StatusAccepted || ss.State != "RETURNING" || !ss.Paused || output.Len() != 0 {
		t.Fatalf("take-back request failed: code=%d state=%s event=%s", w.Code, ss.State, output.String())
	}
	phone := httptest.NewRequest("POST", "/s/token/event", bytes.NewBufferString(`{"type":"return_to_mac","positionSeconds":47.25,"paused":true}`))
	w = httptest.NewRecorder()
	s.phoneEvent(w, phone, ss)
	if w.Code != 200 || ss.State != "MAC_ACTIVE" || ss.Position != 47.25 || ss.Paused || !strings.Contains(output.String(), `"positionSeconds":47.25`) {
		t.Fatalf("latest phone position was not returned: code=%d state=%s position=%v event=%s", w.Code, ss.State, ss.Position, output.String())
	}
}

func TestPhoneCanRequestContinueOnlyWhileMacIsActive(t *testing.T) {
	var output bytes.Buffer
	ss := &session{State: "MAC_ACTIVE", Position: 42, Duration: 100}
	s := &server{s: ss, out: json.NewEncoder(&output)}
	r := httptest.NewRequest("POST", "/s/token/event", bytes.NewBufferString(`{"type":"request_to_phone","positionSeconds":50}`))
	w := httptest.NewRecorder()
	s.phoneEvent(w, r, ss)
	if w.Code != 200 || ss.State != "MAC_ACTIVE" || ss.Position != 42 || !strings.Contains(output.String(), `"type":"request_to_phone"`) {
		t.Fatalf("continue request failed: code=%d state=%s position=%v event=%s", w.Code, ss.State, ss.Position, output.String())
	}
	ss.State = "PHONE_ACTIVE"
	w = httptest.NewRecorder()
	s.phoneEvent(w, httptest.NewRequest("POST", "/s/token/event", bytes.NewBufferString(`{"type":"request_to_phone","positionSeconds":50}`)), ss)
	if w.Code != 409 {
		t.Fatalf("request must be rejected unless Mac is active, got %d", w.Code)
	}
}

func TestSessionBindsToFirstPhoneAndAllowsRescan(t *testing.T) {
	ss := &session{Token: "token", State: "MAC_ACTIVE", Duration: 100, Created: time.Now()}
	s := &server{s: ss, failedTokens: map[string][]time.Time{}}
	open := func(ip string) int {
		r := httptest.NewRequest("GET", "/s/token/", nil)
		r.RemoteAddr = ip + ":1234"
		w := httptest.NewRecorder()
		s.phone(w, r)
		return w.Code
	}
	if code := open("10.0.0.2"); code != 200 || ss.clientIP != "10.0.0.2" {
		t.Fatalf("first phone was not bound: code=%d ip=%q", code, ss.clientIP)
	}
	if code := open("10.0.0.2"); code != 200 {
		t.Fatalf("same phone must be able to scan again, got %d", code)
	}
	if code := open("10.0.0.3"); code != 404 {
		t.Fatalf("a second phone must be rejected, got %d", code)
	}
}

func TestNativePlayActivatesPhone(t *testing.T) {
	var output bytes.Buffer
	ss := &session{State: "PREPARING", Position: 42, Duration: 100, Paused: true}
	s := &server{s: ss, out: json.NewEncoder(&output)}
	r := httptest.NewRequest("POST", "/s/token/event", bytes.NewBufferString(`{"type":"ready","positionSeconds":43,"paused":false}`))
	w := httptest.NewRecorder()
	s.phoneEvent(w, r, ss)
	if w.Code != 200 || ss.State != "PHONE_ACTIVE" || ss.Paused || !strings.Contains(output.String(), `"type":"ready"`) {
		t.Fatalf("native play activation failed: code=%d state=%s paused=%v event=%s", w.Code, ss.State, ss.Paused, output.String())
	}
}

func TestEndImmediatelyInvalidatesSession(t *testing.T) {
	var output bytes.Buffer
	ss := &session{State: "PHONE_ACTIVE", Position: 56, Duration: 100}
	s := &server{secret: "secret", s: ss, out: json.NewEncoder(&output), http: &http.Server{}}
	r := httptest.NewRequest("POST", "/control/end", bytes.NewBufferString(`{}`))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Continue-Secret", "secret")
	w := httptest.NewRecorder()
	s.endSession(w, r)
	if w.Code != 200 || ss.State != "CLOSED" || !strings.Contains(output.String(), `"type":"end"`) {
		t.Fatalf("end failed: code=%d state=%s event=%s", w.Code, ss.State, output.String())
	}
}

func TestExpiredSessionClosesAndNotifiesMac(t *testing.T) {
	var output bytes.Buffer
	ss := &session{State: "PHONE_ACTIVE", Position: 56, Duration: 100, Created: time.Now().Add(-sessionMaxAge - time.Second)}
	s := &server{s: ss, out: json.NewEncoder(&output)}
	if !s.expireSession() || ss.State != "CLOSED" || !ss.Paused || !strings.Contains(output.String(), `"type":"expired"`) {
		t.Fatalf("expiry failed: state=%s paused=%v event=%s", ss.State, ss.Paused, output.String())
	}
}

func TestHLSSubtitlesRestartAtContinuePosition(t *testing.T) {
	ss := &session{Mode: "hardware-transcode", Start: 600, SourcePath: "/movie.mp4", SubtitleIndex: 3, SubtitleDelay: 1.25}
	args := strings.Join(subtitleArgs(ss.SourcePath, ss.SubtitleIndex, ss.SubtitlePath), " ")
	if strings.Contains(args, "-ss") || !strings.Contains(args, "-i /movie.mp4") {
		t.Fatalf("subtitle conversion must preserve the source timeline: %s", args)
	}
	p := filepath.Join(t.TempDir(), "subtitle.vtt")
	input := "WEBVTT\n\n09:58.416 --> 10:00.000\nfirst\n\n10:00.083 --> 10:02.291 align:start\nsecond\n\n10:10.000 --> 10:11.000\nthird\n"
	if err := os.WriteFile(p, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	if err := shiftWebVTT(p, ss.SubtitleDelay-ss.Start); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "00:00:00.000 --> 00:00:01.250\nfirst") || !strings.Contains(got, "00:00:01.333 --> 00:00:03.541 align:start") || !strings.Contains(got, "00:00:11.250 --> 00:00:12.250") {
		t.Fatalf("subtitle timeline was not rebased exactly: %s", got)
	}
}

func TestSubtitleConversionFilesDoNotOverlapGenerations(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, `fmt.Sprintf("subtitle-%d-primary.vtt", ss.Generation)`) || !strings.Contains(text, `fmt.Sprintf("subtitle-%d-secondary.vtt", ss.Generation)`) {
		t.Fatal("each continue generation must convert into fresh subtitle files")
	}
}

func TestPrimaryAndSecondarySubtitlesAreMerged(t *testing.T) {
	d := t.TempDir()
	primary := filepath.Join(d, "primary.vtt")
	secondary := filepath.Join(d, "secondary.vtt")
	out := filepath.Join(d, "subtitle.vtt")
	if err := os.WriteFile(primary, []byte("WEBVTT\n\n00:00:02.000 --> 00:00:03.000\nprimary\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondary, []byte("WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nsecondary\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := mergeWebVTT(primary, secondary, out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if strings.Index(got, "secondary") > strings.Index(got, "primary") || !strings.Contains(got, "line:10% position:50% align:center") {
		t.Fatalf("primary and secondary subtitles were not merged correctly: %s", got)
	}
}

func TestPhoneCannotTakePlaybackBackToMac(t *testing.T) {
	var output bytes.Buffer
	ss := &session{State: "PHONE_ACTIVE", Position: 42, Duration: 100}
	s := &server{s: ss, out: json.NewEncoder(&output)}
	r := httptest.NewRequest("POST", "/s/token/event", bytes.NewBufferString(`{"type":"return_to_mac","positionSeconds":43,"paused":true}`))
	w := httptest.NewRecorder()
	s.phoneEvent(w, r, ss)
	if w.Code != 409 || ss.State != "PHONE_ACTIVE" || output.Len() != 0 {
		t.Fatalf("phone must not control Mac playback: code=%d state=%s event=%s", w.Code, ss.State, output.String())
	}
}

func TestSubtitleFilesAreGenerationSpecific(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "subtitle-2.vtt"), []byte("WEBVTT\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ss := &session{TempDir: d, Generation: 2}
	s := &server{}
	w := httptest.NewRecorder()
	s.subtitle(w, httptest.NewRequest("GET", "/subtitle/2.vtt", nil), ss, "2.vtt")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "WEBVTT") {
		t.Fatalf("current subtitle generation was not served: code=%d body=%q", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.subtitle(w, httptest.NewRequest("GET", "/subtitle/3.vtt", nil), ss, "3.vtt")
	if w.Code != 404 {
		t.Fatalf("future subtitle generation must be rejected, got %d", w.Code)
	}
}

func TestWaitForFileRequiresNonEmptyPlaylist(t *testing.T) {
	p := filepath.Join(t.TempDir(), "index.m3u8")
	if err := os.WriteFile(p, nil, 0600); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = os.WriteFile(p, []byte("#EXTM3U\n"), 0600)
	}()
	if err := waitForFile(p, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestHLSPausesWithPlaybackInsteadOfExpiring(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, `syscall.SIGSTOP`) || !strings.Contains(text, `syscall.SIGCONT`) || !strings.Contains(text, `"-readrate_initial_burst", "4"`) || !strings.Contains(text, `"-hls_time", "1"`) || !strings.Contains(text, `"-hls_list_size", "12"`) {
		t.Fatal("HLS generation must pause and resume with playback")
	}
}
