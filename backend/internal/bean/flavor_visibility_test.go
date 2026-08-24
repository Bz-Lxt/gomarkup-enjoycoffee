package bean_test

import (
	"context"
	"testing"

	"github.com/alkaid/enjoycoffee/internal/bean"
	"github.com/alkaid/enjoycoffee/internal/flavor"
)

type flavorVisibilityRepo struct {
	flavor.Repository
	nodes    []flavor.Node
	taggings []flavor.Tagging
	beanIDs  []int64
}

func (r *flavorVisibilityRepo) ListNodes(context.Context) ([]flavor.Node, error) {
	return append([]flavor.Node(nil), r.nodes...), nil
}

func (r *flavorVisibilityRepo) ListTaggings(context.Context) ([]flavor.Tagging, error) {
	return append([]flavor.Tagging(nil), r.taggings...), nil
}

func (r *flavorVisibilityRepo) ListBeanIDs(context.Context) ([]int64, error) {
	return append([]int64(nil), r.beanIDs...), nil
}

func (r *flavorVisibilityRepo) SetBeanFlavors(_ context.Context, beanID int64, nodeIDs []int64) error {
	next := make([]flavor.Tagging, 0, len(r.taggings)+len(nodeIDs))
	for _, tagging := range r.taggings {
		if tagging.BeanID != beanID {
			next = append(next, tagging)
		}
	}
	for _, nodeID := range nodeIDs {
		next = append(next, flavor.Tagging{BeanID: beanID, NodeID: nodeID})
	}
	r.taggings = next
	return nil
}

type beanVisibilityRepo struct {
	bean.Repository
	item *bean.Bean
}

func (r *beanVisibilityRepo) Get(context.Context, int64) (*bean.Bean, error) {
	copyOfItem := *r.item
	return &copyOfItem, nil
}

func (r *beanVisibilityRepo) CountBrews(context.Context, int64) (int, error) {
	return 0, nil
}

func TestFlavorAssignmentIsVisibleToBeanReadsImmediately(t *testing.T) {
	const (
		beanID    = int64(42)
		oldNodeID = int64(10)
		newNodeID = int64(20)
	)

	ctx := context.Background()
	flavorRepo := &flavorVisibilityRepo{
		nodes: []flavor.Node{
			{ID: oldNodeID, Name: "柑橘"},
			{ID: newNodeID, Name: "花香"},
		},
		taggings: []flavor.Tagging{{BeanID: beanID, NodeID: oldNodeID}},
		beanIDs:  []int64{beanID},
	}
	cache, err := flavor.NewCache(ctx, flavorRepo, 0)
	if err != nil {
		t.Fatalf("initialize flavor cache: %v", err)
	}
	defer cache.Close()

	flavors := flavor.NewService(flavorRepo, cache)
	beans := bean.NewService(&beanVisibilityRepo{
		item: &bean.Bean{ID: beanID, Name: "测试豆"},
	}, flavors, nil, nil)

	before, err := beans.Get(ctx, beanID)
	if err != nil {
		t.Fatalf("get bean before updating flavors: %v", err)
	}
	if len(before.Flavors) != 1 || before.Flavors[0].NodeID != oldNodeID {
		t.Fatalf("fixture should expose old flavor %d, got %+v", oldNodeID, before.Flavors)
	}

	if err := flavors.SetBeanFlavors(ctx, beanID, []int64{newNodeID}); err != nil {
		t.Fatalf("replace bean flavors: %v", err)
	}

	after, err := beans.Get(ctx, beanID)
	if err != nil {
		t.Fatalf("get bean after updating flavors: %v", err)
	}
	if len(after.Flavors) != 1 || after.Flavors[0].NodeID != newNodeID {
		t.Fatalf("successful flavor update must be visible to the next bean read: want node %d, got %+v",
			newNodeID, after.Flavors)
	}
}
