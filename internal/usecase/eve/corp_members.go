package eve

import (
	"context"
	"sort"
	"strings"

	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

func eveCorpMembers(ctx context.Context, a *session.Session, in corpMembersIn) (any, error) {
	if err := rejectUnknownFormat(in.ResponseFormat); err != nil {
		return nil, err
	}
	corp, err := openCorp(ctx, a, fMembers, "", "corporation membership")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.GetAllPages(ctx, esiPath("corporations", esiID(corp.CorporationID), "members"), &corp.Token.CharacterID, nil, pagesESI)
	if err != nil {
		return nil, wrap("eveCorpMembers", err)
	}
	memberIDs := corpMemberIDs(result.Data)
	if len(memberIDs) == 0 {
		return merge(who(corp), map[string]any{fMembers: []any{}, fNote: "ESI returned an empty roster."}), nil
	}
	names, err := a.Resolver.Names(ctx, memberIDs, nil)
	if err != nil {
		return nil, wrap("eveCorpMembers", err)
	}
	rows := corpMemberRows(memberIDs, names, corpMemberRoleMap(ctx, a, corp, concise(in.ResponseFormat)))
	sort.Slice(rows, func(i, k int) bool {
		return strings.ToLower(j.Str(rows[i][fName])) < strings.ToLower(j.Str(rows[k][fName]))
	})
	paged := applyLimit(rows, limitOr(in.Limit, limitLong), "")

	return merge(who(corp), merge(map[string]any{
		"member_count": len(rows), fDataAge: result.StaleNote(),
		fMembers: project(paged.Rows, []string{fName}, concise(in.ResponseFormat)),
	}, paged.fields)), nil
}

func corpMemberIDs(data any) []int {
	var memberIDs []int
	for _, v := range j.Slice(data) {
		if id := j.Int(v); id != 0 {
			memberIDs = append(memberIDs, id)
		}
	}

	return memberIDs
}

func corpMemberRoleMap(ctx context.Context, a *session.Session, corp *character.Corporation, conciseMode bool) map[int][]string {
	roleMap := map[int][]string{}
	if conciseMode || !corp.HasRole(roleDirector) {
		return roleMap
	}
	rolesRes, err := a.ESI.Get(ctx, esiPath("corporations", esiID(corp.CorporationID), "roles"), &corp.Token.CharacterID, nil, nil)
	if err != nil {
		a.Logger.Error("eve: corporation roles roster", "err", err)

		return roleMap
	}
	for _, row := range j.Maps(rolesRes.Data) {
		roleMap[j.Int(row[fCharacterID])] = corpRoleStrings(row[fRoles])
	}

	return roleMap
}

func corpRoleStrings(roles any) []string {
	var rs []string
	for _, r := range j.Slice(roles) {
		if s, ok := r.(string); ok {
			rs = append(rs, s)
		}
	}

	return rs
}

func corpMemberRows(memberIDs []int, names map[int]string, roleMap map[int][]string) []map[string]any {
	rows := make([]map[string]any, 0, len(memberIDs))
	for _, mid := range memberIDs {
		var roles any
		if r := roleMap[mid]; len(r) > 0 {
			roles = r
		}
		rows = append(rows, map[string]any{fName: nameOr(names, mid), fCharacterID: mid, fRoles: roles})
	}

	return rows
}
