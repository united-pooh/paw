package streamma

import (
	"fmt"
	"strings"
)

const (
	TopologyShapeAdaptive = "adaptive"
	TopologyShapeChain    = "chain"
	TopologyShapeTree     = "tree"
	TopologyShapeGraph    = "graph"
)

func NormalizeTopologyShape(shape string) string {
	switch strings.ToLower(strings.TrimSpace(shape)) {
	case TopologyShapeChain:
		return TopologyShapeChain
	case TopologyShapeTree:
		return TopologyShapeTree
	case TopologyShapeGraph:
		return TopologyShapeGraph
	case "", TopologyShapeAdaptive:
		return TopologyShapeAdaptive
	default:
		return strings.ToLower(strings.TrimSpace(shape))
	}
}

func ApplyTopologyShape(agents []TopologyAgent, shape string) (TopologyConfig, error) {
	shape = NormalizeTopologyShape(shape)
	if shape == TopologyShapeAdaptive {
		return TopologyConfig{Agents: cloneTopologyAgents(agents)}, nil
	}
	if len(agents) < 2 {
		return TopologyConfig{}, fmt.Errorf("%s topology requires at least 2 agents", shape)
	}
	out := TopologyConfig{Agents: cloneTopologyAgents(agents)}
	for i := range out.Agents {
		out.Agents[i].Next = nil
	}
	switch shape {
	case TopologyShapeChain:
		for i := 0; i < len(out.Agents)-1; i++ {
			out.Agents[i].Next = []string{out.Agents[i+1].ID}
		}
	case TopologyShapeTree:
		next := make([]string, 0, len(out.Agents)-1)
		for i := 1; i < len(out.Agents); i++ {
			next = append(next, out.Agents[i].ID)
		}
		out.Agents[0].Next = next
	case TopologyShapeGraph:
		for i := 0; i < len(out.Agents)-1; i++ {
			out.Agents[i].Next = append(out.Agents[i].Next, out.Agents[i+1].ID)
		}
		for i := 2; i < len(out.Agents); i++ {
			out.Agents[0].Next = append(out.Agents[0].Next, out.Agents[i].ID)
		}
	default:
		return TopologyConfig{}, fmt.Errorf("unsupported streamma topology shape: %s", shape)
	}
	return out, nil
}

func GraphFromTopologyShape(runID string, stepPolicy StepPolicy, agents []TopologyAgent, shape string) (GraphSpec, error) {
	topology, err := ApplyTopologyShape(agents, shape)
	if err != nil {
		return GraphSpec{}, err
	}
	return GraphFromTopology(runID, stepPolicy, topology)
}

func CriticalPathAgentCount(graph GraphSpec) int {
	compiled, err := compileGraph(graph)
	if err != nil {
		return 0
	}
	order := topologicalAgentOrder(compiled)
	longest := map[string]int{}
	best := 0
	for _, id := range order {
		length := 1
		for _, pred := range compiled.predecessors[id] {
			if candidate := longest[pred] + 1; candidate > length {
				length = candidate
			}
		}
		longest[id] = length
		if length > best {
			best = length
		}
	}
	return best
}

func cloneTopologyAgents(agents []TopologyAgent) []TopologyAgent {
	out := append([]TopologyAgent(nil), agents...)
	for i := range out {
		out[i].Next = append([]string(nil), agents[i].Next...)
	}
	return out
}

func topologicalAgentOrder(graph *compiledGraph) []string {
	if graph == nil {
		return nil
	}
	indegree := map[string]int{}
	for _, id := range graph.agentOrder {
		indegree[id] = len(graph.predecessors[id])
	}
	queue := append([]string(nil), graph.sources...)
	sortStrings(queue)
	var order []string
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		order = append(order, id)
		for _, succ := range graph.successors[id] {
			indegree[succ]--
			if indegree[succ] == 0 {
				queue = append(queue, succ)
				sortStrings(queue)
			}
		}
	}
	return order
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
