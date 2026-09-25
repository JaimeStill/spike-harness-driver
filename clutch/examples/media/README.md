# Media fixtures

The capability scenarios send these files to a model and check its answer against what the
files are known to hold.

## shapes.png

A red circle, a blue square, and a green triangle on white, 480 pixels square. `shapes.go`
draws it with the standard library, so the file can be regenerated exactly:

```bash
go run ./clutch/examples/media/shapes.go
```

## phrase.wav

One speaker saying:

> The access code is seven four two nine.

It is 16 kHz mono and a few seconds long. Gemma 4 E4B loops on clips longer than about 30
seconds. Generate it with espeak-ng:

```bash
sudo pacman -S espeak-ng
espeak-ng -v en-us -s 140 -w /tmp/phrase-raw.wav "The access code is seven four two nine."
ffmpeg -y -i /tmp/phrase-raw.wav -ar 16000 -ac 1 clutch/examples/media/phrase.wav
```

Or record it, saying the phrase within six seconds:

```bash
ffmpeg -y -f pulse -i default -t 6 -ar 16000 -ac 1 clutch/examples/media/phrase.wav
```
