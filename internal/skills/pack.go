package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// PackVersion is the wire format version of a SkillPack. Bump when the
// JSON layout changes in a backwards-incompatible way. Importers reject
// packs with an unknown version rather than guess at intent.
const PackVersion = 1

// Pack is the shareable skill-pack format. A pack is a portable bundle
// of one or more skills that another Mnemos install can import with
// `mnemos skill import <file-or-url>`. The format is JSON so it can be
// hand-read, diffed, and served from a plain URL.
type Pack struct {
	Version   int         `json:"version"`
	CreatedAt time.Time   `json:"created_at"`
	Source    PackSource  `json:"source,omitempty"`
	Skills    []PackSkill `json:"skills"`
}

// PackSource is the optional attribution block. Import doesn't use these
// fields — they're provenance for humans browsing the pack.
type PackSource struct {
	Name    string `json:"name,omitempty"`    // "@voidmode"
	URL     string `json:"url,omitempty"`     // "https://github.com/.../pack.json"
	Project string `json:"project,omitempty"` // optional project name
}

// PackSkill is the wire representation of one skill inside a pack.
// Runtime-only fields (id, use_count, effectiveness, source_sessions) are
// deliberately omitted — they belong to the installation that created
// them, not the shared knowledge.
type PackSkill struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Procedure   string   `json:"procedure"`
	Pitfalls    string   `json:"pitfalls,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// Marshal writes the pack as indented JSON. Indented so packs are
// reviewable in pull requests.
func (p *Pack) Marshal() ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}

// UnmarshalPack reads and validates a pack from r. Unknown versions are
// rejected; packs with zero skills are rejected; empty skill names are
// rejected. Importers should call UnmarshalPack before persisting.
func UnmarshalPack(r io.Reader) (*Pack, error) {
	var p Pack
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("decode pack: %w", err)
	}
	if p.Version == 0 {
		return nil, errors.New("pack: missing version field")
	}
	if p.Version > PackVersion {
		return nil, fmt.Errorf("pack: version %d is newer than supported (%d); upgrade mnemos",
			p.Version, PackVersion)
	}
	if len(p.Skills) == 0 {
		return nil, errors.New("pack: contains no skills")
	}
	for i, s := range p.Skills {
		if s.Name == "" {
			return nil, fmt.Errorf("pack: skill %d has empty name", i)
		}
		if s.Procedure == "" {
			return nil, fmt.Errorf("pack: skill %q has empty procedure", s.Name)
		}
	}
	return &p, nil
}

// BuildPack assembles a Pack from a list of Skills plus an optional
// source attribution. Only the shareable fields are copied.
func BuildPack(src PackSource, skills []Skill) *Pack {
	packSkills := make([]PackSkill, 0, len(skills))
	for _, s := range skills {
		packSkills = append(packSkills, PackSkill{
			Name:        s.Name,
			Description: s.Description,
			Procedure:   s.Procedure,
			Pitfalls:    s.Pitfalls,
			Tags:        append([]string(nil), s.Tags...),
		})
	}
	return &Pack{
		Version:   PackVersion,
		CreatedAt: time.Now().UTC(),
		Source:    src,
		Skills:    packSkills,
	}
}
