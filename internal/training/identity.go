package training

import "context"

// OwnerUserID is the original owner, who holds all data from before accounts
// existed. The owner is recognized by configuration rather than a stored subject,
// so moving to another identity instance only changes that setting.
const OwnerUserID int64 = 1

// noUser is no account, as ids start at 1. Every route sets an identity, so nothing
// should reach it; if one did, its writes would land here, apart from everyone's data.
const noUser int64 = 0

type userKey struct{}

// WithUser records who a request acts as. Only the code that authenticated the
// request may call it.
func WithUser(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, userKey{}, userID)
}

func UserFrom(ctx context.Context) (int64, bool) {
	userID, ok := ctx.Value(userKey{}).(int64)
	return userID, ok
}

// ForRequest returns the service for whoever the request acts as. A request with no
// identity never touches a real user's data, so a route that forgets to authenticate
// fails apart rather than exposing the owner's.
func (s *Services) ForRequest(ctx context.Context) *Service {
	userID, ok := UserFrom(ctx)
	if !ok {
		return s.ForUser(noUser)
	}
	return s.ForUser(userID)
}

// UserResolver says which user a verified subject is. ok is false for someone with
// no account here.
type UserResolver func(ctx context.Context, subject string) (userID int64, ok bool, err error)

// ResolveUser recognizes the owner and anyone who already has an account. With
// provision set, any other subject gets an account of its own, which trusts the
// identity provider to admit only invited people.
func (s *Services) ResolveUser(ownerSubject string, provision bool) UserResolver {
	return func(ctx context.Context, subject string) (int64, bool, error) {
		if subject == "" {
			return 0, false, nil
		}
		if subject == ownerSubject {
			return OwnerUserID, true, nil
		}
		userID, found, err := s.store.UserBySubject(ctx, subject)
		if err != nil || found {
			return userID, found, err
		}
		if !provision {
			return 0, false, nil
		}
		userID, err = s.store.ProvisionUser(ctx, subject)
		if err != nil {
			return 0, false, err
		}
		return userID, true, nil
	}
}
