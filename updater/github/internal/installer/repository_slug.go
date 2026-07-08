package installer

type RepositorySlug struct {
	owner string
	repo  string
}

// Repository interface
var _ Repository = RepositorySlug{}

// NewRepositorySlug creates a RepositorySlug from owner and repo parameters
func NewRepositorySlug(owner, repo string) RepositorySlug {
	return RepositorySlug{
		owner: owner,
		repo:  repo,
	}
}

func (r RepositorySlug) GetSlug() (string, string, error) {
	if r.owner == "" && r.repo == "" {
		return "", "", ErrInvalidSlug
	}
	if r.owner == "" {
		return r.owner, r.repo, ErrIncorrectParameterOwner
	}
	if r.repo == "" {
		return r.owner, r.repo, ErrIncorrectParameterRepo
	}
	return r.owner, r.repo, nil
}
