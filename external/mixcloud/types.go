package mixcloud

import "time"

type apiPage[T any] struct {
	Data   []T       `json:"data"`
	Paging apiPaging `json:"paging"`
	Name   string    `json:"name"`
}

type apiPaging struct {
	Next string `json:"next"`
}

type apiPictures struct {
	Medium     string `json:"medium"`
	Large      string `json:"large"`
	ExtraLarge string `json:"extra_large"`
}

type apiUser struct {
	Key            string      `json:"key"`
	URL            string      `json:"url"`
	Name           string      `json:"name"`
	Username       string      `json:"username"`
	CloudcastCount int         `json:"cloudcast_count"`
	Pictures       apiPictures `json:"pictures"`
}

type apiTag struct {
	Key  string `json:"key"`
	URL  string `json:"url"`
	Name string `json:"name"`
}

type apiCategory struct {
	Key    string `json:"key"`
	URL    string `json:"url"`
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	Format string `json:"format"`
}

type apiCloudcast struct {
	Key         string      `json:"key"`
	URL         string      `json:"url"`
	Name        string      `json:"name"`
	Tags        []apiTag    `json:"tags"`
	CreatedTime time.Time   `json:"created_time"`
	UpdatedTime time.Time   `json:"updated_time"`
	AudioLength int         `json:"audio_length"`
	Pictures    apiPictures `json:"pictures"`
	User        apiUser     `json:"user"`
	IsExclusive bool        `json:"is_exclusive"`
}

type apiPlaylist struct {
	Key            string  `json:"key"`
	URL            string  `json:"url"`
	Name           string  `json:"name"`
	CloudcastCount int     `json:"cloudcast_count"`
	Owner          apiUser `json:"owner"`
}

type apiActivity struct {
	Cloudcasts  []apiCloudcast `json:"cloudcasts"`
	CreatedTime time.Time      `json:"created_time"`
}

type apiErrorEnvelope struct {
	Error struct {
		Type       string `json:"type"`
		Message    string `json:"message"`
		RetryAfter int    `json:"retry_after"`
	} `json:"error"`
}
