# Sonos output

Press `d` and choose **Local** or a discovered Sonos room. Discovery uses the
native Sonos bridge and its cached household seed. Selecting a room attaches
to its coordinator and reads the current track, transport, position, and group
volume. Selection does not start, pause, or change the Sonos queue.

The playlist pane shows up to 200 tracks from the saved Sonos queue and labels
the full queue total. The snapshot stays separate from the current input track.
Use Up/Down to select a saved item, Enter to play it, and `A` to refresh the
snapshot. `d` still opens the output picker. Transport controls and five-second
status updates do not reload the queue or move your selection. If refresh fails,
cached rows stay visible, but Enter is disabled until a refresh succeeds.

In the output picker, Space marks logical rooms without changing playback. `A`
applies exactly the checked rooms, and `s` separates the currently selected
Sonos group. Enter still attaches to a room without changing its group; Esc
cancels the checks. Stereo pairs and home-theater bonds stay intact.

While a Sonos room is selected, play/pause, next/previous, and volume controls
target that room. Previous restarts the current track if it has played for more
than three seconds; press Previous again near the start to go to the prior
track. The local player stops and its queue remains in cliamp. Choosing Local
returns ownership without starting that queue; choose a local track or press
Play to start it deliberately.
Group volume uses Sonos's integer 0–100 scale; cliamp IPC volume values and
responses remain in dB and reflect that scale's quantized setting.

When local Spotify music is playing or buffering, selecting Sonos appends the
current track and the remaining play-next/shuffled/sequential order to the
Sonos queue, then starts that appended segment at the current position. The
whole remaining batch must be Spotify tracks before anything is appended.
Unsupported tracks leave Local playback running. Sonos keeps its existing queue;
tracks appended before cancellation or a partial error remain there. The local
player stops only after Sonos accepts Play. DJ and narrated sessions are not
transferred. Idle, paused, and reconnect selection still attaches read-only.

This output mode does not provide seeking, Spotify Connect output control,
or per-speaker volume. URL, provider, and
queue actions cannot start local audio while Sonos owns output.
Unsupported controls such as seek, speed, EQ, mono, shuffle, repeat, and Stop
are reported as unavailable. The display uses Sonos track, transport, position,
and group volume; local EQ and spectrum visualization are hidden. The local
playlist remains available as a saved queue and is not presented as playing on
the Sonos room.
