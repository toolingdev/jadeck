# Simulator

`ptzsim` stands in for a whole booth so you can try jadeck, build
dashboards or demo it with no gear. It speaks the real protocols, so
jadeck and its add-ons connect to it exactly as they would to the
real things.

| Pretends to be | How | Default address |
|---|---|---|
| PTZOptics cameras (1–n) | VISCA + HTTP (`param.cgi`, live `snapshot.jpg`) | VISCA 15700+, HTTP 18100+ |
| …their video | H.264 over RTSP: a drawn venue; pan, tilt and zoom move the picture; tally border from the switcher | `rtsp://127.0.0.1:18554/camN` (+ `-sd`) |
| vMix | Web Controller API: inputs, preview, cut, fade, streaming/recording | 127.0.0.1:8088 |
| …its program output | the live input's picture (fades included), published to jadeck's Program feed | `rtsp://127.0.0.1:8554/program` |
| Proclaim | network control: a service plan, slides, blank screen; the slide also shows on the venue's screen | 127.0.0.1:52195 |
| Behringer WING | OSC: probe, channel mutes (read back), names, faders | UDP 2223 |

## Run it

Needs **ffmpeg** (`brew install ffmpeg`) and **MediaMTX** (jadeck unpacks
one into `<data>/video/bin`; or pass `--mediamtx`).

1. Start jadeck as usual (not `--mock`; that brings its own simulated devices).
2. Start the simulator and let it set jadeck up:

```bash
go run ./cmd/ptzsim --cameras 4 --setup http://127.0.0.1:8080
```

`--setup` adds the cameras, the Program feed, the vMix / Proclaim / WING
connections (with mixer favorites) and a **Simulator** dashboard. Run it
without `--setup` the next time; jadeck remembers.

Options: `--cameras N` (the first 8 get names: Wide, Lectern, Band,
Singers, Audience, Keys, Drums, Screen), `--fps`, `--host 0.0.0.0` to
reach it from other machines, and each device's address. `ptzsim -h`
lists them.
