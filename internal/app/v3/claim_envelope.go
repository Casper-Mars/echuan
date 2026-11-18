package v3

import (
	"context"

	storedb "github.com/Casper-Mars/echuan/internal/store"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

// ProductRef is the dispatch slice of a product.
type ProductRef struct {
	ProductID string
	Name      string
	GitURL    string
}

// ClaimEnvelope is everything a worker needs to build a task prompt: the task,
// its project background, the product repository, input attachments, finished
// dependencies and whether this is the project's last dev task.
type ClaimEnvelope struct {
	Task             storev3.Task
	Project          storev3.Project
	Product          ProductRef
	Inputs           []storev3.MaterialRef
	Upstream         []storev3.UpstreamRef
	HasDownstreamDev bool
}

// ClaimEnvelope enriches a claimed task. Reading is done outside the claim
// transaction: the task is already leased to this worker, so the data is
// stable enough for prompt construction.
func (s *Service) ClaimEnvelope(ctx context.Context, task storev3.Task) (ClaimEnvelope, error) {
	project, err := storev3.GetProject(ctx, s.st.Read(), task.ProjectCode)
	if err != nil {
		return ClaimEnvelope{}, Translate(err)
	}
	productRow, err := storedb.GetProductByID(ctx, s.st.Read(), project.ProductID)
	if err != nil {
		return ClaimEnvelope{}, Translate(err)
	}

	inputs, err := storev3.ListProjectInputs(ctx, s.st.Read(), project.ID)
	if err != nil {
		return ClaimEnvelope{}, Translate(err)
	}
	taskInputs, err := storev3.ListTaskInputs(ctx, s.st.Read(), task.ID)
	if err != nil {
		return ClaimEnvelope{}, Translate(err)
	}
	inputs = append(inputs, taskInputs...)

	upstream, err := storev3.ListUpstreamOutputs(ctx, s.st.Read(), task.ID)
	if err != nil {
		return ClaimEnvelope{}, Translate(err)
	}
	hasDownstream, err := storev3.HasDownstreamDev(ctx, s.st.Read(), task.ID)
	if err != nil {
		return ClaimEnvelope{}, Translate(err)
	}

	return ClaimEnvelope{
		Task:    task,
		Project: project,
		Product: ProductRef{
			ProductID: productRow.ProductID,
			Name:      productRow.Name,
			GitURL:    productRow.GitURL,
		},
		Inputs:           inputs,
		Upstream:         upstream,
		HasDownstreamDev: hasDownstream,
	}, nil
}
