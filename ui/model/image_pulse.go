package model

// imagePulse alternates the invisible reset at the end of an image
// placeholder line. Bubble Tea redraws only the lines whose bytes changed;
// a static kitty placeholder would stop being re-emitted and the terminal
// would drop the image. The plugin panes solve this inside cliamp.image.block;
// the selector receives a pre-rendered block, so the core has to keep it
// alive on each frame.
var imagePulse bool

func imageResetSuffix() string {
	if imagePulse {
		return "\x1b[0m"
	}
	return "\x1b[0;0m"
}
