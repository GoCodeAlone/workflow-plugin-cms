package store_test

import (
	"github.com/GoCodeAlone/workflow-plugin-cms/store"
	"github.com/GoCodeAlone/workflow-plugin-cms/store/storetest"
	"testing"
)

func TestMemoryPageBatchContract(t *testing.T) {
	storetest.PageBatches(t, store.NewMemoryPageStore(), 1, 2)
}
