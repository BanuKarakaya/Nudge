package tracker

import "context"

// CardPage exposes list names and assignment alongside the same authorized card
// page, so the assistant need not read each card to answer a list/assignment query.
type ListedCard struct {
	Item
	ListName              string `json:"list_name"`
	AssignedToCurrentUser bool   `json:"assigned_to_current_user"`
}
type CardList struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type CardPage struct {
	BoardID       string       `json:"board_id"`
	CurrentUserID string       `json:"current_user_id"`
	Lists         []CardList   `json:"lists"`
	Items         []ListedCard `json:"items"`
	NextCursor    string       `json:"next_cursor,omitempty"`
	Complete      bool         `json:"complete"`
}

func (s *Service) Cards(ctx context.Context, id Identity, resource, cursor string) (CardPage, error) {
	if s.Kind() != "trello" || len(cursor) > 4096 {
		return CardPage{}, ErrArguments
	}
	c, r, err := s.resource(ctx, id, resource)
	if err != nil {
		return CardPage{}, err
	}
	lists, ok := s.provider.(interface {
		CreateOptions(context.Context, string, Resource, string) (CreateOptions, error)
	})
	if !ok {
		return CardPage{}, ErrUnavailable
	}
	options, err := lists.CreateOptions(ctx, c.Credentials.Access, r, "")
	if err != nil {
		return CardPage{}, err
	}
	page, err := s.provider.Items(ctx, c.Credentials.Access, r, cursor)
	if err != nil {
		return CardPage{}, err
	}
	result := CardPage{BoardID: r.ID, CurrentUserID: c.Account.ID, Lists: []CardList{}, Items: []ListedCard{}, NextCursor: page.NextCursor, Complete: page.NextCursor == ""}
	names := map[string]string{}
	for _, list := range options.Options {
		names[list.ID] = list.Name
		result.Lists = append(result.Lists, CardList{list.ID, list.Name})
	}
	for _, item := range page.Items {
		card := ListedCard{Item: item, ListName: names[item.StatusID]}
		for _, member := range item.MemberIDs {
			if c.Account.ID != "" && member == c.Account.ID {
				card.AssignedToCurrentUser = true
				break
			}
		}
		result.Items = append(result.Items, card)
	}
	return result, nil
}
