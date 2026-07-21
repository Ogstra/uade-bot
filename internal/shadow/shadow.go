package shadow

import "github.com/ogs/uade-bot/internal/uade"

type Divergence struct {
	Node, Go uade.OutcomeCode
	Critical bool
}
type Comparator struct{ Divergences []Divergence }

func (c *Comparator) Compare(node, goResult uade.Outcome) Divergence {
	d := Divergence{Node: node.Code, Go: goResult.Code, Critical: node.Code != goResult.Code}
	if d.Critical {
		c.Divergences = append(c.Divergences, d)
	}
	return d
}
func (c Comparator) Ready() bool { return len(c.Divergences) == 0 }
