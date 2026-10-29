package asana

// Project contains the stored fields of an Asana project.
type Project struct {
	GID      string `json:"gid"`
	Name     string `json:"name"`
	Archived bool   `json:"archived"`
}
