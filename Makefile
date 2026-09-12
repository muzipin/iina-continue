GO_BIN ?= go
.PHONY: test helper ffmpeg pack verify all
test:
	cd helper && "$(GO_BIN)" test ./...
	node --test plugin/tests/*.test.mjs
helper:
	GO_BIN="$(GO_BIN)" ./scripts/build-helper.sh
ffmpeg:
	./scripts/build-ffmpeg.sh
pack: helper ffmpeg
	./scripts/pack.sh
verify: pack
	./scripts/verify-package.sh
all: test verify
