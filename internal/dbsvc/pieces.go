package dbsvc

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"skifity/internal/api"
	"skifity/internal/dbsvc/engine"
	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/settings"
	"skifity/internal/store"
	"skifity/internal/templates"
)

// A database's connection, piece by piece.
//
// A link gives an app the connection string, and can give it the host, the
// port, the database's name, the user and the password as variables of their
// own too, for software that asks for those and has no setting for a URL.
// They are the link's: written with the URL, rewritten with it when the
// password changes, and removed with it. They used to be written once, by a
// template install, as variables the link knew nothing about, so a password
// change gave an app a new connection string and left its password piece
// holding the old one — which stopped working as soon as the change took the
// old one away, while the panel reported that it had worked.

// pieceValues is what each piece of a connection is, from its credentials.
func pieceValues(credentials api.DatabaseCredentials) map[string]string {
	return map[string]string{
		engine.PieceHost: credentials.Host, engine.PiecePort: strconv.Itoa(credentials.Port),
		engine.PieceName: credentials.Database, engine.PieceUser: credentials.Username,
		engine.PiecePassword: credentials.Password,
	}
}

// checkPieces refuses pieces that cannot be delivered: a name a container
// cannot carry, two parts of the connection under one name, or a piece the
// engine does not have on its own.
func checkPieces(engineName, varName string, pieces store.LinkPieces) error {
	kind, known := engine.Lookup(engineName)
	seen := map[string]bool{varName: true}
	for _, pair := range pieces.Pairs() {
		if _, err := kube.SanitiseEnvKey(pair.Variable); err != nil {
			return errdoc.BadRequest(err.Error())
		}
		if seen[pair.Variable] {
			return errdoc.DatabasePiecesClash(pair.Variable)
		}
		seen[pair.Variable] = true
		if known && !kind.OffersPiece(pair.Piece) {
			return errdoc.DatabasePieceNotOffered(kind.Title, pair.Piece)
		}
	}
	return nil
}

// linkVariables are the variables a link writes, sealed for the app: the
// connection string, a secret because it carries the password, then each
// piece, of which only the password is a secret.
func (m *Manager) linkVariables(credentials api.DatabaseCredentials, link store.DatabaseLink) ([]store.VariableChange, error) {
	seal := func(key, value string, secret bool) (store.VariableChange, error) {
		sealed, err := m.keyring.Seal([]byte(value), "variable:"+link.AppID+":"+key)
		if err != nil {
			return store.VariableChange{}, err
		}
		return store.VariableChange{Variable: store.Variable{AppID: link.AppID, Key: key, IsSecret: secret}, Sealed: sealed}, nil
	}
	url, err := seal(link.VarName, credentials.URL, true)
	if err != nil {
		return nil, err
	}
	out := []store.VariableChange{url}
	values := pieceValues(credentials)
	for _, pair := range link.Pieces.Pairs() {
		change, err := seal(pair.Variable, values[pair.Piece], pair.Piece == engine.PiecePassword)
		if err != nil {
			return nil, err
		}
		out = append(out, change)
	}
	return out, nil
}

// leftBehind are the variables a link wrote before that it does not write
// now: its URL's old name, and pieces it no longer delivers.
func leftBehind(before, after store.DatabaseLink) []string {
	var out []string
	for _, name := range before.Variables() {
		if !slices.Contains(after.Variables(), name) {
			out = append(out, name)
		}
	}
	return out
}

// TemplateFinder finds the template an app was installed from, wherever the
// install found it: the built-in catalogue or a team's own. false is a
// template that is no longer there.
type TemplateFinder func(ctx context.Context, installed store.AppTemplate) (templates.Template, bool)

// AttachTemplatePieces gives the links template installs made before a link
// kept its pieces the pieces those installs wrote beside them, so a password
// change reaches them too. It runs once, and records that it did: an app is
// matched to its template by the variables the template declares, and an app
// whose variables no longer match — somebody renamed or removed one — is left
// as it is. A piece holding a password the database no longer has is given
// the one it has, and the app is rolled out with it.
//
// It answers how many links were given pieces. One that could not be is
// said in the log, and the whole is tried again at the next start; the links
// already done are not touched again.
func (m *Manager) AttachTemplatePieces(ctx context.Context, find TemplateFinder) (int, error) {
	done, _, err := m.db.GetSetting(ctx, settings.KeyTemplatePiecesAttached)
	if err != nil {
		return 0, err
	}
	if done != "" {
		return 0, nil
	}
	installed, err := m.db.ListAppTemplates(ctx)
	if err != nil {
		return 0, err
	}
	attached, failed := 0, false
	for _, record := range installed {
		tpl, ok := find(ctx, record)
		if !ok {
			continue
		}
		n, err := m.attachTemplatePieces(ctx, record, tpl)
		attached += n
		if err != nil {
			failed = true
			m.log.Warn("could not give a template app's database link its pieces; trying again at the next start",
				"app", record.AppID, "template", record.TemplateID, "error", err)
		}
	}
	if failed {
		return attached, nil
	}
	return attached, m.db.SetSetting(ctx, settings.KeyTemplatePiecesAttached, store.Now(), false, "")
}

// attachTemplatePieces does it for one app.
func (m *Manager) attachTemplatePieces(ctx context.Context, installed store.AppTemplate, tpl templates.Template) (int, error) {
	links, err := m.db.ListLinksForApp(ctx, installed.AppID)
	if err != nil {
		return 0, err
	}
	rows, err := m.db.ListVariables(ctx, installed.AppID)
	if err != nil {
		return 0, err
	}
	have := map[string]string{}
	for _, row := range rows {
		value := "" // a value that cannot be read is one to write again
		if plain, err := m.keyring.Open(row.Sealed, "variable:"+installed.AppID+":"+row.Key); err == nil {
			value = string(plain)
		}
		have[row.Key] = value
	}

	attached := 0
	for _, spec := range tpl.Databases {
		pieces := store.LinkPieces{Host: spec.Vars.Host, Port: spec.Vars.Port, Name: spec.Vars.Name,
			User: spec.Vars.User, Password: spec.Vars.Password}
		if len(pieces.Pairs()) == 0 || !slices.Contains(spec.LinkTo, installed.Service) {
			continue
		}
		varName := spec.VarName
		if varName == "" {
			varName = engine.DefaultVariable(spec.Engine)
		}
		index := slices.IndexFunc(links, func(l store.DatabaseLink) bool { return l.VarName == varName })
		if index < 0 || len(links[index].Pieces.Pairs()) > 0 {
			continue
		}
		link := links[index]
		matches := true
		for _, name := range pieces.Names() {
			if _, ok := have[name]; !ok {
				matches = false
			}
		}
		if !matches {
			continue
		}
		credentials, err := m.Credentials(ctx, link.DatabaseID)
		if err != nil {
			return attached, err
		}
		if checkPieces(credentials.Engine, link.VarName, pieces) != nil {
			continue
		}
		link.Pieces = pieces
		changes, err := m.linkVariables(credentials, link)
		if err != nil {
			return attached, err
		}
		// Only what differs: the rest already says what it should, and
		// writing it again would be a change that is not one.
		// The pieces follow the URL, in the order of their pairs.
		values, pairs := pieceValues(credentials), pieces.Pairs()
		var stale []store.VariableChange
		for i, change := range changes[1:] {
			if have[change.Variable.Key] != values[pairs[i].Piece] {
				stale = append(stale, change)
			}
		}
		if len(stale) > 0 {
			if err := m.db.ChangeVariables(ctx, installed.AppID, stale, nil); err != nil {
				return attached, err
			}
		}
		if err := m.db.SetLinkPieces(ctx, link.DatabaseID, installed.AppID, pieces); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return attached, err
		}
		attached++
		if len(stale) > 0 && m.deployer != nil {
			if err := m.deployer.Sync(ctx, installed.AppID); err != nil {
				m.log.Warn("could not roll out a template app's corrected database pieces; its next deploy will",
					"app", installed.AppID, "error", err)
			}
		}
	}
	return attached, nil
}
