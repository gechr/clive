package installer

type Repository interface {
	GetSlug() (string, string, error)
}
