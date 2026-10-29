package asana

// User contains the stored fields of an Asana user.
type User struct {
	GID   string `json:"gid"`
	Name  string `json:"name"`
	Email string `json:"email"`
}
