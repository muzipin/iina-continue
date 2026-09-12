package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	version       = "0.1.1"
	maxBody       = 32 << 10
	sessionMaxAge = 4 * time.Hour
	heartbeatLate = 10 * time.Second
)

//go:embed web/*
var webFiles embed.FS

type mediaInfo struct {
	Streams []struct {
		Index     int    `json:"index"`
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
	} `json:"streams"`
}

type createRequest struct {
	SourcePath             string  `json:"sourcePath"`
	Title                  string  `json:"title"`
	DurationSeconds        float64 `json:"durationSeconds"`
	StartPositionSeconds   float64 `json:"startPositionSeconds"`
	Quality                string  `json:"quality"`
	AudioIndex             int     `json:"audioIndex"`
	SubtitleIndex          int     `json:"subtitleIndex"`
	SubtitlePath           string  `json:"subtitlePath"`
	SubtitleDelay          float64 `json:"subtitleDelay"`
	SecondarySubtitleIndex int     `json:"secondarySubtitleIndex"`
	SecondarySubtitlePath  string  `json:"secondarySubtitlePath"`
	SecondarySubtitleDelay float64 `json:"secondarySubtitleDelay"`
}

type phoneEvent struct {
	Type     string  `json:"type"`
	Position float64 `json:"positionSeconds"`
	Paused   bool    `json:"paused"`
}

type continueRequest struct {
	Position               float64 `json:"positionSeconds"`
	Quality                string  `json:"quality"`
	AudioIndex             int     `json:"audioIndex"`
	SubtitleIndex          int     `json:"subtitleIndex"`
	SubtitlePath           string  `json:"subtitlePath"`
	SubtitleDelay          float64 `json:"subtitleDelay"`
	SecondarySubtitleIndex int     `json:"secondarySubtitleIndex"`
	SecondarySubtitlePath  string  `json:"secondarySubtitlePath"`
	SecondarySubtitleDelay float64 `json:"secondarySubtitleDelay"`
}

type event struct {
	Revision int64   `json:"revision"`
	Type     string  `json:"type"`
	Position float64 `json:"positionSeconds,omitempty"`
	Paused   bool    `json:"paused"`
}

type session struct {
	Token, State, Title, Quality, Mode, MediaURL string
	SourcePath, TempDir                          string
	SubtitleURL, SubtitleWarning                 string
	AudioIndex, SubtitleIndex                    int
	SubtitlePath                                 string
	SecondarySubtitleIndex                       int
	SecondarySubtitlePath                        string
	Duration, Start, Position                    float64
	SubtitleDelay, SecondarySubtitleDelay        float64
	Revision, Generation                         int64
	Created, LastHeartbeat                       time.Time
	Cmd                                          *exec.Cmd
	cancel                                       context.CancelFunc
	disconnectSent                               bool
	clientIP                                     string
	Paused                                       bool
	encoderPaused                                bool
}

type server struct {
	mu           sync.Mutex
	outMu        sync.Mutex
	out          *json.Encoder
	secret       string
	ffmpeg       string
	ffprobe      string
	lanIP        string
	port         int
	s            *session
	http         *http.Server
	failedTokens map[string][]time.Time
	shutdownOnce sync.Once
	parentPID    int
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "self-check" {
		fmt.Println(`{"ok":true,"version":"` + version + `"}`)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "client" {
		if err := runClient(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	ffmpeg, ffprobe, parentPID, sessionJSON := flags()
	secret, err := randomHex(32)
	if err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		log.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	s := &server{secret: secret, ffmpeg: ffmpeg, ffprobe: ffprobe, lanIP: localIP(), port: port, failedTokens: map[string][]time.Time{}, parentPID: parentPID, out: json.NewEncoder(os.Stdout)}
	mux := http.NewServeMux()
	mux.HandleFunc("/control/continue", s.continueSession)
	mux.HandleFunc("/control/take-back", s.takeBack)
	mux.HandleFunc("/control/end", s.endSession)
	mux.HandleFunc("/control/stop", s.stop)
	mux.HandleFunc("/s/", s.phone)
	s.http = &http.Server{Handler: limitHeaders(mux), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	go s.watchLifetime()
	ready := map[string]any{"event": "ready", "version": version, "port": port, "controlSecret": secret, "lanIP": s.lanIP}
	if sessionJSON != "" {
		var q createRequest
		if err := json.Unmarshal([]byte(sessionJSON), &q); err != nil {
			ready["error"] = "media_info"
		} else if session, err := s.newSession(q); err != nil {
			ready["error"] = err.Error()
		} else {
			ready["session"] = session
		}
	}
	s.emit(ready)
	if ready["error"] != nil {
		_ = ln.Close()
		return
	}
	if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	s.closeSession()
}

func runClient(args []string) error {
	return performClient(args, &http.Client{Timeout: 8 * time.Second}, os.Stdout)
}

func performClient(args []string, client *http.Client, output io.Writer) error {
	method, target, secret, body := "GET", "", "", "{}"
	for i := 0; i+1 < len(args); i += 2 {
		switch args[i] {
		case "--method":
			method = strings.ToUpper(args[i+1])
		case "--url":
			target = args[i+1]
		case "--secret":
			secret = args[i+1]
		case "--body":
			body = args[i+1]
		default:
			return fmt.Errorf("unknown client argument %s", args[i])
		}
	}
	if target == "" || secret == "" || (method != "GET" && method != "POST") {
		return errors.New("client requires a loopback URL, secret, and GET or POST method")
	}
	u, err := neturl.Parse(target)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" {
		return errors.New("client only accepts http://127.0.0.1 URLs")
	}
	var payload io.Reader
	if method == "POST" {
		payload = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(method, target, payload)
	if err != nil {
		return err
	}
	req.Header.Set("X-Continue-Secret", secret)
	if method == "POST" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("local control request failed: %w", err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return err
	}
	if len(b) > maxBody {
		return errors.New("local control response too large")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("local control returned HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	_, err = output.Write(b)
	return err
}

func flags() (string, string, int, string) {
	ffmpeg, ffprobe, sessionJSON := "ffmpeg", "ffprobe", ""
	parentPID := 0
	for i := 1; i+1 < len(os.Args); i += 2 {
		switch os.Args[i] {
		case "--ffmpeg":
			ffmpeg = os.Args[i+1]
		case "--ffprobe":
			ffprobe = os.Args[i+1]
		case "--parent":
			parentPID, _ = strconv.Atoi(os.Args[i+1])
		case "--session":
			sessionJSON = os.Args[i+1]
		default:
			log.Fatalf("unknown argument %s", os.Args[i])
		}
	}
	return ffmpeg, ffprobe, parentPID, sessionJSON
}

func (s *server) emit(v any) {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	_ = s.out.Encode(v)
}

func (s *server) watchLifetime() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-sigs:
			s.shutdown()
			return
		case <-t.C:
			s.checkDisconnect()
			if s.expireSession() {
				s.shutdown()
				return
			}
			if s.parentPID > 0 && syscall.Kill(s.parentPID, 0) != nil {
				s.shutdown()
				return
			}
		}
	}
}

func (s *server) expireSession() bool {
	s.mu.Lock()
	ss := s.s
	if ss == nil || ss.State == "CLOSED" || time.Since(ss.Created) < sessionMaxAge {
		s.mu.Unlock()
		return false
	}
	ss.State = "CLOSED"
	ss.Paused = true
	ss.Revision++
	e := event{Revision: ss.Revision, Type: "expired", Position: ss.Position, Paused: true}
	s.stopFFmpegLocked(ss)
	s.mu.Unlock()
	s.emit(map[string]any{"event": "phone", "data": e})
	return true
}

func (s *server) checkDisconnect() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.s != nil && s.s.State == "PHONE_ACTIVE" && !s.s.disconnectSent && time.Since(s.s.LastHeartbeat) > heartbeatLate {
		s.s.Revision++
		s.s.disconnectSent = true
		s.s.Paused = true
		s.pauseFFmpegLocked(s.s)
		e := event{Revision: s.s.Revision, Type: "disconnected", Position: s.s.Position, Paused: true}
		s.emit(map[string]any{"event": "phone", "data": e})
	}
}
func (s *server) shutdown() {
	s.shutdownOnce.Do(func() {
		if s.http == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.http.Shutdown(ctx)
		_ = s.http.Close()
	})
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func localIP() string {
	cs, _ := net.Interfaces()
	for _, in := range cs {
		if in.Flags&net.FlagUp == 0 || in.Flags&net.FlagLoopback != 0 {
			continue
		}
		as, _ := in.Addrs()
		for _, a := range as {
			ip, _, _ := net.ParseCIDR(a.String())
			if ip != nil && ip.To4() != nil && (ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
				return ip.String()
			}
		}
	}
	return "127.0.0.1"
}

func limitHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.Header) > 40 || r.ContentLength > maxBody {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method != want {
		w.Header().Set("Allow", want)
		http.Error(w, "method not allowed", 405)
		return false
	}
	return true
}
func loopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	return err == nil && net.ParseIP(host).IsLoopback()
}
func (s *server) authorized(r *http.Request) bool {
	return loopback(r) && r.Header.Get("X-Continue-Secret") == s.secret
}

func (s *server) control(w http.ResponseWriter, r *http.Request) bool {
	if !method(w, r, "POST") {
		return false
	}
	if !s.authorized(r) {
		http.Error(w, "forbidden", 403)
		return false
	}
	return true
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		http.Error(w, "invalid JSON", 400)
		return false
	}
	return true
}

func validateCreate(q createRequest) error {
	if q.SourcePath == "" || q.Title == "" {
		return errors.New("missing media")
	}
	if q.DurationSeconds <= 0 || q.StartPositionSeconds < 0 || q.StartPositionSeconds > q.DurationSeconds+2 {
		return errors.New("invalid position")
	}
	if q.Quality != "auto" && q.Quality != "original" && q.Quality != "1080p" && q.Quality != "720p" {
		return errors.New("invalid quality")
	}
	if q.SubtitleDelay < -600 || q.SubtitleDelay > 600 || q.SecondarySubtitleDelay < -600 || q.SecondarySubtitleDelay > 600 {
		return errors.New("invalid subtitle delay")
	}
	fi, err := os.Lstat(q.SourcePath)
	if err != nil {
		return errors.New("cannot read media")
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		return errors.New("media must be a regular file")
	}
	return nil
}

func (s *server) newSession(q createRequest) (map[string]any, error) {
	if err := validateCreate(q); err != nil {
		return nil, err
	}
	id, err := randomHex(16)
	if err != nil {
		return nil, errors.New("secure_session")
	}
	token, err := randomToken()
	if err != nil {
		return nil, errors.New("secure_session")
	}
	tmp, err := os.MkdirTemp("", "iina-continue-"+id+"-")
	if err != nil {
		return nil, errors.New("temporary_session")
	}
	info, err := s.probe(q.SourcePath)
	if err != nil {
		os.RemoveAll(tmp)
		return nil, errors.New("unrecognized_media")
	}
	mode := chooseMode(q, info)
	ss := &session{Token: token, State: "PREPARING", Title: q.Title, Quality: q.Quality, Mode: mode, SourcePath: q.SourcePath, TempDir: tmp, AudioIndex: q.AudioIndex, SubtitleIndex: q.SubtitleIndex, SubtitlePath: q.SubtitlePath, SubtitleDelay: q.SubtitleDelay, SecondarySubtitleIndex: q.SecondarySubtitleIndex, SecondarySubtitlePath: q.SecondarySubtitlePath, SecondarySubtitleDelay: q.SecondarySubtitleDelay, Duration: q.DurationSeconds, Start: q.StartPositionSeconds, Position: q.StartPositionSeconds, Revision: 1, Generation: 1, Created: time.Now(), LastHeartbeat: time.Now()}
	if mode == "direct" {
		ss.MediaURL = fmt.Sprintf("/s/%s/media", token)
	} else {
		ss.MediaURL = fmt.Sprintf("/s/%s/hls/%d/index.m3u8", token, ss.Generation)
	}
	s.mu.Lock()
	s.s = ss
	s.mu.Unlock()
	s.prepareSubtitle(ss, info)
	if mode != "direct" {
		if err := s.startHLS(ss, info, q.StartPositionSeconds); err != nil {
			s.mu.Lock()
			ss.State = "ERROR"
			ss.Revision++
			e := event{Revision: ss.Revision, Type: "error"}
			s.emit(map[string]any{"event": "phone", "data": e})
			s.mu.Unlock()
		}
	}
	url := fmt.Sprintf("http://%s:%d/s/%s/", s.lanIP, s.port, token)
	return map[string]any{"state": ss.State, "url": url, "mode": mode, "revision": ss.Revision}, nil
}

func (s *server) probe(path string) (mediaInfo, error) {
	c := exec.Command(s.ffprobe, "-v", "error", "-show_streams", "-show_format", "-of", "json", path)
	b, err := c.Output()
	if err != nil {
		return mediaInfo{}, err
	}
	var x mediaInfo
	err = json.Unmarshal(b, &x)
	return x, err
}
func codecs(x mediaInfo) (string, string) {
	var v, a string
	for _, t := range x.Streams {
		if t.CodecType == "video" && v == "" {
			v = t.CodecName
		}
		if t.CodecType == "audio" && a == "" {
			a = t.CodecName
		}
	}
	return v, a
}

func subtitleArgs(source string, index int, external string) []string {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	input := source
	if external != "" {
		input = external
	}
	args = append(args, "-i", input, "-map")
	if external != "" {
		args = append(args, "0:s:0")
	} else {
		args = append(args, fmt.Sprintf("0:%d", index))
	}
	return append(args, "-c:s", "webvtt")
}

func (s *server) prepareSubtitle(ss *session, info mediaInfo) {
	ss.SubtitleURL, ss.SubtitleWarning = "", ""
	primary, warning1 := s.convertSubtitle(ss, info, ss.SubtitleIndex, ss.SubtitlePath, ss.SubtitleDelay, fmt.Sprintf("subtitle-%d-primary.vtt", ss.Generation))
	secondary, warning2 := s.convertSubtitle(ss, info, ss.SecondarySubtitleIndex, ss.SecondarySubtitlePath, ss.SecondarySubtitleDelay, fmt.Sprintf("subtitle-%d-secondary.vtt", ss.Generation))
	warnings := []string{}
	for _, warning := range []string{warning1, warning2} {
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}
	ss.SubtitleWarning = strings.Join(warnings, " ")
	if primary == "" && secondary == "" {
		return
	}
	out := filepath.Join(ss.TempDir, fmt.Sprintf("subtitle-%d.vtt", ss.Generation))
	if err := mergeWebVTT(primary, secondary, out); err != nil {
		ss.SubtitleWarning = "subtitle_merge"
		return
	}
	ss.SubtitleURL = fmt.Sprintf("/s/%s/subtitle/%d.vtt", ss.Token, ss.Generation)
}

func (s *server) convertSubtitle(ss *session, info mediaInfo, index int, external string, delay float64, name string) (string, string) {
	if index < 0 && external == "" {
		return "", ""
	}
	codec := ""
	for _, stream := range info.Streams {
		if stream.Index == index {
			codec = stream.CodecName
			break
		}
	}
	if strings.Contains(codec, "pgs") || strings.Contains(codec, "dvd_subtitle") || strings.Contains(codec, "dvb_subtitle") {
		return "", "image_subtitle"
	}
	out := filepath.Join(ss.TempDir, name)
	if external != "" {
		fi, err := os.Lstat(external)
		if err != nil || !fi.Mode().IsRegular() || fi.Mode()&os.ModeSymlink != 0 {
			return "", "subtitle_unreadable"
		}
	}
	args := append(subtitleArgs(ss.SourcePath, index, external), out)
	if err := exec.Command(s.ffmpeg, args...).Run(); err != nil {
		return "", "subtitle_conversion"
	}
	offset := delay
	if ss.Mode != "direct" {
		offset -= ss.Start
	}
	if err := shiftWebVTT(out, offset); err != nil {
		return "", "subtitle_timeline"
	}
	return out, ""
}

type vttCue struct {
	start float64
	text  string
}

func mergeWebVTT(primary, secondary, out string) error {
	cues := []vttCue{}
	for _, item := range []struct {
		path      string
		secondary bool
	}{{primary, false}, {secondary, true}} {
		if item.path == "" {
			continue
		}
		b, err := os.ReadFile(item.path)
		if err != nil {
			return err
		}
		for _, block := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n\n") {
			lines := strings.Split(block, "\n")
			for i, line := range lines {
				left, _, ok := strings.Cut(line, " --> ")
				start, valid := parseVTTTime(strings.TrimSpace(left))
				if !ok || !valid {
					continue
				}
				if item.secondary && !strings.Contains(line, " line:") {
					lines[i] += " line:10% position:50% align:center"
				}
				cues = append(cues, vttCue{start: start, text: strings.Join(lines, "\n")})
				break
			}
		}
	}
	sort.SliceStable(cues, func(i, j int) bool { return cues[i].start < cues[j].start })
	blocks := []string{"WEBVTT"}
	for _, cue := range cues {
		blocks = append(blocks, cue.text)
	}
	return os.WriteFile(out, []byte(strings.Join(blocks, "\n\n")+"\n"), 0600)
}

func shiftWebVTT(path string, offset float64) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	blocks := strings.Split(text, "\n\n")
	out := make([]string, 0, len(blocks))
	for _, block := range blocks {
		lines := strings.Split(block, "\n")
		drop := false
		for i, line := range lines {
			left, right, ok := strings.Cut(line, " --> ")
			if !ok {
				continue
			}
			endToken, settings, _ := strings.Cut(right, " ")
			start, okStart := parseVTTTime(strings.TrimSpace(left))
			end, okEnd := parseVTTTime(strings.TrimSpace(endToken))
			if !okStart || !okEnd {
				continue
			}
			start += offset
			end += offset
			if end <= 0 {
				drop = true
				break
			}
			if start < 0 {
				start = 0
			}
			lines[i] = formatVTTTime(start) + " --> " + formatVTTTime(end)
			if settings != "" {
				lines[i] += " " + settings
			}
			break
		}
		if !drop {
			out = append(out, strings.Join(lines, "\n"))
		}
	}
	return os.WriteFile(path, []byte(strings.Join(out, "\n\n")), 0600)
}

func parseVTTTime(v string) (float64, bool) {
	parts := strings.Split(v, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return 0, false
	}
	seconds, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil {
		return 0, false
	}
	minutes, err := strconv.Atoi(parts[len(parts)-2])
	if err != nil {
		return 0, false
	}
	total := float64(minutes*60) + seconds
	if len(parts) == 3 {
		hours, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, false
		}
		total += float64(hours * 3600)
	}
	return total, true
}

func formatVTTTime(v float64) string {
	ms := int64(v*1000 + 0.5)
	hours := ms / 3600000
	ms %= 3600000
	minutes := ms / 60000
	ms %= 60000
	seconds := ms / 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", hours, minutes, seconds, ms%1000)
}
func chooseMode(q createRequest, x mediaInfo) string {
	v, a := codecs(x)
	ext := strings.ToLower(filepath.Ext(q.SourcePath))
	if (ext == ".mp4" || ext == ".mov" || ext == ".m4v") && v == "h264" && a == "aac" && (q.Quality == "auto" || q.Quality == "original") {
		return "direct"
	}
	if v == "h264" && (q.Quality == "auto" || q.Quality == "original") {
		if a == "aac" {
			return "remux"
		}
		return "audio-transcode"
	}
	return "hardware-transcode"
}

func (s *server) startHLS(ss *session, info mediaInfo, at float64) error {
	s.stopFFmpegLocked(ss)
	dir := filepath.Join(ss.TempDir, strconv.FormatInt(ss.Generation, 10))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	_, a := codecs(info)
	audioMap := "0:a:0?"
	if ss.AudioIndex >= 0 {
		audioMap = fmt.Sprintf("0:%d?", ss.AudioIndex)
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "warning", "-readrate", "1", "-readrate_initial_burst", "4", "-ss", fmt.Sprintf("%.3f", at), "-i", ss.SourcePath, "-map", "0:v:0", "-map", audioMap, "-sn", "-dn"}
	if ss.Mode == "remux" || ss.Mode == "audio-transcode" {
		args = append(args, "-c:v", "copy")
	} else {
		args = append(args, "-c:v", "h264_videotoolbox", "-allow_sw", "0", "-realtime", "1", "-b:v", "5000k")
		if ss.Quality == "auto" || ss.Quality == "1080p" {
			args = append(args, "-vf", "scale='min(iw,1920)':'min(ih,1080)':force_original_aspect_ratio=decrease:force_divisible_by=2")
		}
		if ss.Quality == "720p" {
			args = append(args, "-vf", "scale='min(iw,1280)':'min(ih,720)':force_original_aspect_ratio=decrease:force_divisible_by=2")
		}
	}
	if a == "aac" {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", "aac", "-b:a", "192k", "-ac", "2")
	}
	playlist := filepath.Join(dir, "index.m3u8")
	args = append(args, "-muxdelay", "0", "-muxpreload", "0", "-f", "hls", "-hls_time", "1", "-hls_list_size", "12", "-hls_delete_threshold", "2", "-hls_flags", "delete_segments+independent_segments", "-hls_segment_filename", filepath.Join(dir, "seg_%06d.ts"), playlist)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, s.ffmpeg, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	logFile, _ := os.Create(filepath.Join(ss.TempDir, "ffmpeg.log"))
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		cancel()
		logFile.Close()
		return err
	}
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, 10)
	ss.Cmd = cmd
	ss.cancel = cancel
	go func(gen int64) {
		err := cmd.Wait()
		logFile.Close()
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.s == ss && ss.Generation == gen && ss.State != "CLOSED" && err != nil && ctx.Err() == nil {
			ss.State = "ERROR"
			ss.Revision++
			e := event{Revision: ss.Revision, Type: "error"}
			s.emit(map[string]any{"event": "phone", "data": e})
		}
	}(ss.Generation)
	go s.watchCache(ss, ss.Generation, dir)
	if err := waitForFile(playlist, 8*time.Second); err != nil {
		s.stopFFmpegLocked(ss)
		return err
	}
	s.pauseFFmpegLocked(ss)
	return nil
}

func waitForFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if st, err := os.Stat(path); err == nil && st.Size() > 0 {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("media preparation timed out")
}

func (s *server) watchCache(ss *session, gen int64, dir string) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for range t.C {
		s.mu.Lock()
		if s.s != ss || ss.Generation != gen || ss.State == "CLOSED" {
			s.mu.Unlock()
			return
		}
		if directorySize(dir) > 256<<20 {
			ss.State = "ERROR"
			ss.Revision++
			e := event{Revision: ss.Revision, Type: "error"}
			s.emit(map[string]any{"event": "phone", "data": e})
			s.stopFFmpegLocked(ss)
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
	}
}
func directorySize(root string) int64 {
	var n int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			n += info.Size()
		}
		return nil
	})
	return n
}

func (s *server) continueSession(w http.ResponseWriter, r *http.Request) {
	if !s.control(w, r) {
		return
	}
	var q continueRequest
	if !decodeBody(w, r, &q) {
		return
	}
	if q.Quality != "auto" && q.Quality != "original" && q.Quality != "1080p" && q.Quality != "720p" {
		http.Error(w, "invalid quality", 400)
		return
	}
	s.mu.Lock()
	ss := s.s
	if ss == nil || (ss.State != "MAC_ACTIVE" && ss.State != "PREPARING") || q.Position < 0 || q.Position > ss.Duration+2 || q.SubtitleDelay < -600 || q.SubtitleDelay > 600 || q.SecondarySubtitleDelay < -600 || q.SecondarySubtitleDelay > 600 {
		s.mu.Unlock()
		http.Error(w, "invalid state transition", 409)
		return
	}
	info, err := s.probe(ss.SourcePath)
	if err != nil {
		s.mu.Unlock()
		http.Error(w, "cannot read media", 422)
		return
	}
	mode := chooseMode(createRequest{SourcePath: ss.SourcePath, Quality: q.Quality}, info)
	ss.State, ss.Quality, ss.Mode = "PREPARING", q.Quality, mode
	ss.Start, ss.Position, ss.Generation, ss.Paused = q.Position, q.Position, ss.Generation+1, false
	ss.AudioIndex, ss.SubtitleIndex, ss.SubtitlePath, ss.SubtitleDelay = q.AudioIndex, q.SubtitleIndex, q.SubtitlePath, q.SubtitleDelay
	ss.SecondarySubtitleIndex, ss.SecondarySubtitlePath, ss.SecondarySubtitleDelay = q.SecondarySubtitleIndex, q.SecondarySubtitlePath, q.SecondarySubtitleDelay
	ss.Revision++
	s.prepareSubtitle(ss, info)
	if mode == "direct" {
		s.stopFFmpegLocked(ss)
		ss.MediaURL = fmt.Sprintf("/s/%s/media", ss.Token)
	} else {
		ss.MediaURL = fmt.Sprintf("/s/%s/hls/%d/index.m3u8", ss.Token, ss.Generation)
		if err := s.startHLS(ss, info, q.Position); err != nil {
			ss.State = "ERROR"
		}
	}
	result := map[string]any{"url": fmt.Sprintf("http://%s:%d/s/%s/", s.lanIP, s.port, ss.Token), "mode": ss.Mode, "state": ss.State, "revision": ss.Revision}
	s.mu.Unlock()
	jsonOut(w, 200, result)
	s.emit(map[string]any{"event": "continued", "session": result})
}

func (s *server) takeBack(w http.ResponseWriter, r *http.Request) {
	if !s.control(w, r) {
		return
	}
	s.mu.Lock()
	ss := s.s
	if ss == nil || ss.State != "PHONE_ACTIVE" {
		s.mu.Unlock()
		http.Error(w, "invalid state transition", 409)
		return
	}
	ss.State = "RETURNING"
	ss.Paused = true
	ss.Revision++
	revision := ss.Revision
	s.pauseFFmpegLocked(ss)
	s.mu.Unlock()
	jsonOut(w, http.StatusAccepted, map[string]bool{"ok": true})
	go func() {
		time.Sleep(1500 * time.Millisecond)
		s.mu.Lock()
		if s.s != ss || ss.State != "RETURNING" || ss.Revision != revision {
			s.mu.Unlock()
			return
		}
		ss.State = "MAC_ACTIVE"
		ss.Paused = false
		ss.Revision++
		e := event{Revision: ss.Revision, Type: "return_to_mac", Position: ss.Position, Paused: false}
		s.stopFFmpegLocked(ss)
		s.mu.Unlock()
		s.emit(map[string]any{"event": "phone", "data": e})
	}()
}

func (s *server) endSession(w http.ResponseWriter, r *http.Request) {
	if !s.control(w, r) {
		return
	}
	s.mu.Lock()
	ss := s.s
	if ss == nil || ss.State == "CLOSED" {
		s.mu.Unlock()
		http.Error(w, "invalid state transition", 409)
		return
	}
	ss.State = "CLOSED"
	ss.Revision++
	e := event{Revision: ss.Revision, Type: "end", Position: ss.Position, Paused: ss.Paused}
	s.stopFFmpegLocked(ss)
	s.mu.Unlock()
	jsonOut(w, 200, map[string]bool{"ok": true})
	s.emit(map[string]any{"event": "phone", "data": e})
	go func() {
		time.Sleep(1200 * time.Millisecond)
		s.shutdown()
	}()
}

func (s *server) stop(w http.ResponseWriter, r *http.Request) {
	if !s.control(w, r) {
		return
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
	go func() {
		time.Sleep(50 * time.Millisecond)
		s.shutdown()
	}()
}

func (s *server) phone(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/s/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	token := parts[0]
	sub := ""
	if len(parts) > 1 {
		sub = strings.Join(parts[1:], "/")
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	s.mu.Lock()
	ss := s.s
	valid := ss != nil && ss.Token == token && ss.State != "CLOSED" && time.Since(ss.Created) < sessionMaxAge && (ss.clientIP == "" || ss.clientIP == host)
	if valid && sub == "" && r.Method == "GET" && ss.clientIP == "" {
		ss.clientIP = host
	}
	s.mu.Unlock()
	if !valid {
		if s.badToken(r) {
			http.Error(w, "too many attempts", http.StatusTooManyRequests)
			return
		}
		http.NotFound(w, r)
		return
	}
	switch {
	case sub == "":
		s.player(w, r)
	case sub == "player.css" || sub == "player.js":
		s.playerAsset(w, r, sub)
	case sub == "state":
		s.publicState(w, r, ss)
	case sub == "event":
		s.phoneEvent(w, r, ss)
	case sub == "media":
		s.direct(w, r, ss)
	case strings.HasPrefix(sub, "subtitle/"):
		s.subtitle(w, r, ss, strings.TrimPrefix(sub, "subtitle/"))
	case strings.HasPrefix(sub, "hls/"):
		s.hls(w, r, ss, strings.TrimPrefix(sub, "hls/"))
	default:
		http.NotFound(w, r)
	}
}
func (s *server) badToken(r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	xs := s.failedTokens[host][:0]
	for _, t := range s.failedTokens[host] {
		if now.Sub(t) < time.Minute {
			xs = append(xs, t)
		}
	}
	s.failedTokens[host] = append(xs, now)
	return len(xs) >= 10
}
func (s *server) player(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; media-src 'self'; connect-src 'self'; img-src 'self' data:")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	b, _ := webFiles.ReadFile("web/player.html")
	_, _ = w.Write(b)
}
func (s *server) playerAsset(w http.ResponseWriter, r *http.Request, name string) {
	if !method(w, r, "GET") {
		return
	}
	if name == "player.css" {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	}
	b, err := webFiles.ReadFile("web/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(b)
}
func (s *server) publicState(w http.ResponseWriter, r *http.Request, ss *session) {
	if !method(w, r, "GET") {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	jsonOut(w, 200, map[string]any{"state": ss.State, "title": ss.Title, "durationSeconds": ss.Duration, "startPositionSeconds": ss.Start, "lastKnownPositionSeconds": ss.Position, "mediaUrl": ss.MediaURL, "subtitleUrl": ss.SubtitleURL, "warning": ss.SubtitleWarning, "mode": ss.Mode, "generation": ss.Generation, "revision": ss.Revision, "paused": ss.Paused})
}
func (s *server) phoneEvent(w http.ResponseWriter, r *http.Request, ss *session) {
	if !method(w, r, "POST") {
		return
	}
	var e phoneEvent
	if !decodeBody(w, r, &e) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.s != ss {
		http.NotFound(w, r)
		return
	}
	if e.Position < 0 || e.Position > ss.Duration+2 {
		http.Error(w, "invalid position", 400)
		return
	}
	allowed := false
	switch e.Type {
	case "connected":
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		allowed = ss.State != "CLOSED" && (ss.clientIP == "" || ss.clientIP == host)
		if allowed {
			ss.clientIP = host
		}
	case "ready":
		allowed = ss.State == "PREPARING"
		if allowed {
			ss.State = "PHONE_ACTIVE"
			ss.Paused = e.Paused
			if e.Paused {
				s.pauseFFmpegLocked(ss)
			} else {
				s.resumeFFmpegLocked(ss)
			}
		}
	case "play":
		allowed = ss.State == "PHONE_ACTIVE"
		if allowed {
			ss.Paused = false
			s.resumeFFmpegLocked(ss)
		}
	case "pause":
		allowed = ss.State == "PHONE_ACTIVE"
		if allowed {
			ss.Paused = true
			s.pauseFFmpegLocked(ss)
		}
	case "heartbeat":
		allowed = ss.State == "PHONE_ACTIVE"
		if allowed {
			ss.Paused = e.Paused
			if e.Paused {
				s.pauseFFmpegLocked(ss)
			} else {
				s.resumeFFmpegLocked(ss)
			}
		}
	case "request_to_phone":
		allowed = ss.State == "MAC_ACTIVE"
	case "return_to_mac":
		allowed = ss.State == "RETURNING"
		if allowed {
			ss.State = "MAC_ACTIVE"
			ss.Paused = false
			s.stopFFmpegLocked(ss)
		}
	case "seek":
		allowed = ss.State == "PHONE_ACTIVE"
		if allowed {
			ss.Paused = e.Paused
		}
	case "end":
		allowed = true
		ss.State = "CLOSED"
	}
	if !allowed {
		http.Error(w, "invalid state transition", 409)
		return
	}
	if e.Type != "connected" && e.Type != "request_to_phone" {
		ss.Position = e.Position
	}
	ss.LastHeartbeat = time.Now()
	ss.disconnectSent = false
	ss.Revision++
	phoneUpdate := event{Revision: ss.Revision, Type: e.Type, Position: e.Position, Paused: ss.Paused}
	if e.Type == "seek" && ss.Mode != "direct" {
		ss.Generation++
		ss.Start = e.Position
		ss.MediaURL = fmt.Sprintf("/s/%s/hls/%d/index.m3u8", ss.Token, ss.Generation)
		info := mustProbe(s, ss.SourcePath)
		s.prepareSubtitle(ss, info)
		_ = s.startHLS(ss, info, e.Position)
		if !ss.Paused {
			s.resumeFFmpegLocked(ss)
		}
	}
	if e.Type == "end" {
		s.stopFFmpegLocked(ss)
	}
	jsonOut(w, 200, map[string]any{"ok": true, "revision": ss.Revision, "generation": ss.Generation})
	if e.Type == "connected" || e.Type == "ready" || e.Type == "request_to_phone" || e.Type == "return_to_mac" || e.Type == "end" {
		s.emit(map[string]any{"event": "phone", "data": phoneUpdate})
		if e.Type == "end" {
			go func() {
				time.Sleep(1200 * time.Millisecond)
				s.shutdown()
			}()
		}
	}
}
func mustProbe(s *server, p string) mediaInfo { x, _ := s.probe(p); return x }
func (s *server) direct(w http.ResponseWriter, r *http.Request, ss *session) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", 405)
		return
	}
	f, err := os.Open(ss.SourcePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	http.ServeContent(w, r, filepath.Base(ss.SourcePath), st.ModTime(), f)
}
func (s *server) subtitle(w http.ResponseWriter, r *http.Request, ss *session, name string) {
	if !method(w, r, "GET") {
		return
	}
	if !strings.HasSuffix(name, ".vtt") {
		http.NotFound(w, r)
		return
	}
	gen, err := strconv.ParseInt(strings.TrimSuffix(name, ".vtt"), 10, 64)
	if err != nil || gen < 1 || gen > ss.Generation {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	http.ServeFile(w, r, filepath.Join(ss.TempDir, "subtitle-"+name))
}
func (s *server) hls(w http.ResponseWriter, r *http.Request, ss *session, rel string) {
	if !method(w, r, "GET") {
		return
	}
	clean := filepath.Clean(rel)
	if clean != rel || strings.Contains(clean, "..") || strings.HasPrefix(clean, "/") {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(clean, "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	gen, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || gen != ss.Generation {
		http.NotFound(w, r)
		return
	}
	name := parts[1]
	if !strings.HasSuffix(name, ".m3u8") && !strings.HasSuffix(name, ".ts") {
		http.NotFound(w, r)
		return
	}
	if t := mime.TypeByExtension(filepath.Ext(name)); t != "" {
		w.Header().Set("Content-Type", t)
	}
	http.ServeFile(w, r, filepath.Join(ss.TempDir, parts[0], name))
}

func (s *server) stopFFmpegLocked(ss *session) {
	if ss.cancel != nil {
		ss.cancel()
		ss.cancel = nil
	}
	if ss.Cmd != nil && ss.Cmd.Process != nil {
		_ = syscall.Kill(-ss.Cmd.Process.Pid, syscall.SIGTERM)
	}
	ss.Cmd = nil
	ss.encoderPaused = false
}

func (s *server) pauseFFmpegLocked(ss *session) {
	if ss.Cmd == nil || ss.Cmd.Process == nil || ss.encoderPaused {
		return
	}
	if syscall.Kill(-ss.Cmd.Process.Pid, syscall.SIGSTOP) == nil {
		ss.encoderPaused = true
	}
}

func (s *server) resumeFFmpegLocked(ss *session) {
	if ss.Cmd == nil || ss.Cmd.Process == nil || !ss.encoderPaused {
		return
	}
	if syscall.Kill(-ss.Cmd.Process.Pid, syscall.SIGCONT) == nil {
		ss.encoderPaused = false
	}
}
func (s *server) closeSession() { s.mu.Lock(); defer s.mu.Unlock(); s.closeSessionLocked() }
func (s *server) closeSessionLocked() {
	if s.s == nil {
		return
	}
	s.stopFFmpegLocked(s.s)
	dir := s.s.TempDir
	s.s = nil
	if strings.HasPrefix(filepath.Base(dir), "iina-continue-") {
		_ = os.RemoveAll(dir)
	}
}
