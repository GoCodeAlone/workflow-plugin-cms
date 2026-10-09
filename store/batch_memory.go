package store

import (
	"context"
	"time"
)

func (s *MemoryPageStore) statesLocked(tenantID int64) []PageState {
	var pages []*Page
	for _, p := range s.pages {
		if p.TenantID == tenantID {
			pages = append(pages, p)
		}
	}
	return States(pages)
}

func (s *MemoryPageStore) ReadPageState(_ context.Context, tenantID int64) (PageSet, error) {
	if tenantID <= 0 {
		return PageSet{}, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return PageSet{Scope: s.scopeLocked(tenantID), Revision: s.revisions[tenantID], Pages: s.statesLocked(tenantID)}, nil
}

func (s *MemoryPageStore) ApplyPageBatch(ctx context.Context, tenantID int64, batch PageBatch) (PageBatchReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyBatchLocked(ctx, tenantID, batch, false)
}

func (s *MemoryPageStore) RollbackPageBatch(ctx context.Context, tenantID int64, receipt PageBatchReceipt) (PageBatchReceipt, error) {
	batch, err := RollbackBatch(receipt)
	if err != nil {
		return PageBatchReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyBatchLocked(ctx, tenantID, batch, true)
}

func (s *MemoryPageStore) applyBatchLocked(ctx context.Context, tenantID int64, batch PageBatch, restore bool) (PageBatchReceipt, error) {
	if tenantID <= 0 {
		return PageBatchReceipt{}, ErrBatchInvalid
	}
	before := s.statesLocked(tenantID)
	state := PageSet{Scope: s.scopeLocked(tenantID), Revision: s.revisions[tenantID], Pages: before}
	if err := CheckPageBatch(state, batch, restore); err != nil {
		return PageBatchReceipt{}, err
	}
	result := PageBatchReceipt{TargetScope: state.Scope, BeforeRevision: state.Revision, Before: before, Mapping: map[string]int64{}}
	now := time.Now().UTC()
	for _, m := range batch.Mutations {
		if m.Kind == "delete" {
			delete(s.pages, m.TargetID)
			result.Mapping[m.Key] = m.TargetID
			continue
		}
		id, version := m.TargetID, m.ExpectedVersion+1
		if m.Kind == "create" {
			s.nextID++
			id = s.nextID
			version = 1
		}
		if m.Kind == "restore" {
			version = m.ExpectedVersion
			if id > s.nextID {
				s.nextID = id
			}
		}
		p := m.Content.Page(tenantID, id, version)
		p.CreatedAt = now
		p.UpdatedAt = now
		if old := s.pages[id]; old != nil {
			p.CreatedAt = old.CreatedAt
		}
		s.pages[id] = p
		result.Mapping[m.Key] = id
	}
	result.After = s.statesLocked(tenantID)
	s.revisions[tenantID]++
	result.AfterRevision = s.revisions[tenantID]
	old, saved := ChangedPageStates(result.Before, result.After)
	operation := "batch.apply"
	if restore {
		operation = "batch.rollback"
	}
	s.recordHistoryLocked(ctx, tenantID, operation, old, saved)
	result.Seal()
	return result, nil
}
