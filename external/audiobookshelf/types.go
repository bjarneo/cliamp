package audiobookshelf

const (
	mediaTypeBook    = "book"
	mediaTypePodcast = "podcast"
)

// Library is one Audiobookshelf library.
type Library struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
}

// LibraryItem is one book or podcast show.
type LibraryItem struct {
	ID        string `json:"id"`
	LibraryID string `json:"libraryId"`
	MediaType string `json:"mediaType"`
	AddedAt   int64  `json:"addedAt"`
	Media     Media  `json:"media"`
}

// Media holds the playable content of a library item.
type Media struct {
	Metadata    Metadata     `json:"metadata"`
	Duration    float64      `json:"duration"`
	NumTracks   int          `json:"numTracks"`
	NumEpisodes int          `json:"numEpisodes"`
	Tracks      []AudioTrack `json:"tracks"`
	Chapters    []Chapter    `json:"chapters"`
	Episodes    []Episode    `json:"episodes"`
}

// Metadata holds item metadata. Books populate AuthorName, podcasts Author.
type Metadata struct {
	Title         string   `json:"title"`
	AuthorName    string   `json:"authorName"`
	Author        string   `json:"author"`
	NarratorName  string   `json:"narratorName"`
	PublishedYear string   `json:"publishedYear"`
	Authors       []NameID `json:"authors"`
}

// NameID is an id/name pair.
type NameID struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AudioTrack is one audio file placed on the item's timeline.
type AudioTrack struct {
	Index       int      `json:"index"`
	StartOffset float64  `json:"startOffset"`
	Duration    float64  `json:"duration"`
	Title       string   `json:"title"`
	Ino         string   `json:"ino"`
	Metadata    FileMeta `json:"metadata"`
}

// FileMeta holds file-level metadata.
type FileMeta struct {
	Filename string `json:"filename"`
}

// Chapter is one chapter marker on a book's timeline.
type Chapter struct {
	ID    int     `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Title string  `json:"title"`
}

// Episode is one podcast episode.
type Episode struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Index       int        `json:"index"`
	PublishedAt int64      `json:"publishedAt"`
	AudioFile   AudioFile  `json:"audioFile"`
	AudioTrack  AudioTrack `json:"audioTrack"`
}

// AudioFile is one file on disk.
type AudioFile struct {
	Ino      string   `json:"ino"`
	Duration float64  `json:"duration"`
	Metadata FileMeta `json:"metadata"`
}

// Author is a book author.
type Author struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	NumBooks     int           `json:"numBooks"`
	LibraryItems []LibraryItem `json:"libraryItems"`
}

// MediaProgress is a stored listening position.
type MediaProgress struct {
	LibraryItemID string  `json:"libraryItemId"`
	EpisodeID     string  `json:"episodeId"`
	CurrentTime   float64 `json:"currentTime"`
	Duration      float64 `json:"duration"`
	IsFinished    bool    `json:"isFinished"`
}

type librariesResponse struct {
	Libraries []Library `json:"libraries"`
}

type itemsResponse struct {
	Results []LibraryItem `json:"results"`
	Total   int           `json:"total"`
	Page    int           `json:"page"`
}

type authorsResponse struct {
	Authors []Author `json:"authors"`
}

type searchHit struct {
	LibraryItem LibraryItem `json:"libraryItem"`
}

type searchResponse struct {
	Book    []searchHit `json:"book"`
	Podcast []searchHit `json:"podcast"`
}

type meResponse struct {
	ID            string          `json:"id"`
	MediaProgress []MediaProgress `json:"mediaProgress"`
}

type loginResponse struct {
	User struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	} `json:"user"`
	AccessToken string `json:"accessToken"`
}
