package coding

import (
	"context"
	"net/url"
	"strconv"
)

type apiBranch struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
		ID  string `json:"id"`
	} `json:"commit"`
}

func (v apiBranch) branch(kind string) (Branch, error) {
	sha := v.Commit.SHA
	if kind == "gitlab" {
		sha = v.Commit.ID
	}
	if !ValidBranch(v.Name) || !gitObjectID.MatchString(sha) {
		return Branch{}, ErrUnavailable
	}
	return Branch{Name: v.Name, SHA: sha}, nil
}

func (g GitHub) GetBranch(ctx context.Context, token string, r Repository, name string) (Branch, error) {
	if !r.Valid() || r.Kind() != "github" || !ValidBranch(name) {
		return Branch{}, ErrArguments
	}
	var v apiBranch
	if err := changeAPI(ctx, g.HTTPClient, "api.github.com", token, "GET", "https://api.github.com/repos/"+r.FullName+"/branches/"+url.PathEscape(name), nil, &v); err != nil {
		return Branch{}, err
	}
	if v.Name != name {
		return Branch{}, ErrChangeMoved
	}
	return v.branch("github")
}
func (g GitLab) GetBranch(ctx context.Context, token string, r Repository, name string) (Branch, error) {
	if !r.Valid() || r.Kind() != "gitlab" || !ValidBranch(name) {
		return Branch{}, ErrArguments
	}
	var v apiBranch
	if err := changeAPI(ctx, g.HTTPClient, "gitlab.com", token, "GET", "https://gitlab.com/api/v4/projects/"+strconv.FormatInt(r.ID, 10)+"/repository/branches/"+url.PathEscape(name), nil, &v); err != nil {
		return Branch{}, err
	}
	if v.Name != name {
		return Branch{}, ErrChangeMoved
	}
	return v.branch("gitlab")
}
func (g GitHub) ListBranches(ctx context.Context, token string, r Repository, page int) ([]Branch, error) {
	if !r.Valid() || r.Kind() != "github" || page < 1 || page > 1000 {
		return nil, ErrArguments
	}
	var values []apiBranch
	err := changeAPI(ctx, g.HTTPClient, "api.github.com", token, "GET", "https://api.github.com/repos/"+r.FullName+"/branches?per_page=100&page="+strconv.Itoa(page), nil, &values)
	if err != nil {
		return nil, err
	}
	return branchValues(values, "github")
}
func (g GitLab) ListBranches(ctx context.Context, token string, r Repository, page int) ([]Branch, error) {
	if !r.Valid() || r.Kind() != "gitlab" || page < 1 || page > 1000 {
		return nil, ErrArguments
	}
	var values []apiBranch
	err := changeAPI(ctx, g.HTTPClient, "gitlab.com", token, "GET", "https://gitlab.com/api/v4/projects/"+strconv.FormatInt(r.ID, 10)+"/repository/branches?per_page=100&page="+strconv.Itoa(page), nil, &values)
	if err != nil {
		return nil, err
	}
	return branchValues(values, "gitlab")
}
func branchValues(values []apiBranch, kind string) ([]Branch, error) {
	result := make([]Branch, 0, len(values))
	for _, v := range values {
		b, err := v.branch(kind)
		if err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, nil
}
