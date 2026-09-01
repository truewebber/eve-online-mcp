package eve

import (
	"context"
	"fmt"

	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

func eveCorpWallet(ctx context.Context, a *session.Session, in corpWalletIn) (any, error) {
	corp, err := openCorp(ctx, a, fWallets, fWallets, "corporation wallets")
	if err != nil {
		return nil, err
	}
	if err := rejectUnknownFormat(in.ResponseFormat); err != nil {
		return nil, err
	}
	kind, err := pickEnum(fKind, in.Kind, vBalances, vBalances, fJournal, fTransactions, vBoth)
	if err != nil {
		return nil, err
	}
	div := in.Division
	if div == 0 {
		div = 1
	}
	divs := corpDivisions(ctx, a, corp)
	if kind == vBalances {
		return corpWalletBalances(ctx, a, corp, divs)
	}

	return corpWalletMovements(ctx, a, corpWalletMove{corp: corp, in: in, kind: kind, div: div, divs: divs})
}

type walletBalanceRows struct {
	rows  []map[string]any
	total float64
}

func corpWalletRows(data any, names map[int]string) walletBalanceRows {
	wallets := j.Maps(data)
	rows := make([]map[string]any, 0, len(wallets))
	total := 0.0
	for _, w := range wallets {
		rows = append(rows, map[string]any{
			fDivision: w[fDivision], fName: walletLabel(j.Int(w[fDivision]), names),
			"balance": isk(w["balance"]), "balance_isk": w["balance"],
		})
		total += j.Float(w["balance"])
	}

	return walletBalanceRows{rows, total}
}

func corpWalletBalances(ctx context.Context, a *session.Session, corp *character.Corporation, divs map[string]map[int]string) (any, error) {
	wallets, err := a.ESI.Get(ctx, esiPath("corporations", esiID(corp.CorporationID), "wallets"), &corp.Token.CharacterID, nil, nil)
	if err != nil {
		return nil, wrap("corpWalletBalances", err)
	}
	bal := corpWalletRows(wallets.Data, divs[fWallet])

	return merge(who(corp), map[string]any{
		"wallet_total": isk(bal.total), fDataAge: wallets.StaleNote(), fWallets: bal.rows,
		fNote: "Pass kind='journal' or kind='transactions' with a division (1-7) to see movements. ESI retains about 30 days.",
	}), nil
}

type corpWalletMove struct {
	corp *character.Corporation
	in   corpWalletIn
	kind string
	div  int
	divs map[string]map[int]string
}

func corpWalletMovements(ctx context.Context, a *session.Session, move corpWalletMove) (any, error) {
	out := merge(who(move.corp), map[string]any{
		fDivision: move.div, "division_name": walletLabel(move.div, move.divs[fWallet]),
		fPeriod: "last ~30 days (ESI retention limit)",
	})
	if move.kind == fJournal || move.kind == vBoth {
		sec, err := corpWalletJournal(ctx, a, move.corp, move.div, move.in)
		if err != nil {
			return nil, err
		}
		out["journal_section"] = sec
	}
	if move.kind == fTransactions || move.kind == vBoth {
		sec, err := transactionSection(ctx, a, txQuery{
			path: esiPath("corporations", esiID(move.corp.CorporationID), "wallets", esiID(move.div), "transactions"),
			cid:  move.corp.Token.CharacterID, offset: move.in.Offset,
			limit: limitOr(move.in.Limit, limitDefault), conciseMode: concise(move.in.ResponseFormat),
		})
		if err != nil {
			return nil, err
		}
		out["transactions_section"] = sec
	}
	if move.kind == fJournal {
		sec := j.Map(out["journal_section"])
		delete(out, "journal_section")

		return merge(out, sec), nil
	}
	if move.kind == fTransactions {
		sec := j.Map(out["transactions_section"])
		delete(out, "transactions_section")

		return merge(out, sec), nil
	}

	return out, nil
}

func corpWalletJournal(ctx context.Context, a *session.Session, corp *character.Corporation, div int, in corpWalletIn) (map[string]any, error) {
	res, err := a.ESI.GetAllPages(ctx, esiPath("corporations", esiID(corp.CorporationID), "wallets", esiID(div), "journal"), &corp.Token.CharacterID, nil, pagesShort)
	if err != nil {
		return nil, wrap("corpWalletJournal", err)
	}

	return summarizeJournal(journalSummary{
		data: res.Data, stale: res.StaleNote(), truncated: res.Truncated, pageCap: pagesShort,
		refType: in.RefType, offset: in.Offset, limit: limitOr(in.Limit, limitDefault),
		conciseMode: concise(in.ResponseFormat), divisionNote: fmt.Sprintf("division %d", div),
	})
}
