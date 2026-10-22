# Sonos output

Press `d` and choose **Local** or a discovered Sonos room. Discovery uses the
native Sonos bridge and its cached household seed. Selecting a room attaches
to its coordinator and reads the current track, transport, position, and group
volume. Selection does not start, pause, or change the Sonos queue.

While a Sonos room is selected, play/pause, next/previous, and volume controls
target that room. Previous restarts the current track if it has played for more
than three seconds; press Previous again near the start to go to the prior
track. The local player stops and its queue remains in cliamp. Choosing Local
returns ownership without starting that queue; choose a local track or press
Play to start it deliberately.
Group volume uses Sonos's integer 0–100 scale; cliamp IPC volume values and
responses remain in dB and reflect that scale's quantized setting.

This output mode does not provide Sonos queue browsing, seeking, grouping,
Spotify Connect output control, local-to-Sonos handoff, or per-speaker volume. URL,
provider, and queue actions cannot start local audio while Sonos owns output.
Unsupported controls such as seek, speed, EQ, mono, shuffle, repeat, and Stop
are reported as unavailable. The display uses Sonos track, transport, position,
and group volume; local EQ and spectrum visualization are hidden. The local
playlist remains available as a saved queue and is not presented as playing on
the Sonos room.
