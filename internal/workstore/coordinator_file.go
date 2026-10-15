package workstore

import "context"

func (c *Coordinator) filePath(key string) bool {
	for _, root := range c.Roots {
		if root.Path == key {
			return root.File
		}
	}
	return false
}

func (c *Coordinator) capture(target BatchTarget) (Tree, error) {
	s := c.store(target.Path)
	if target.File {
		return s.CaptureFile(s.Root)
	}
	return s.Capture(s.Root)
}

func coordinatedTransition(s *Store, ctx context.Context, file bool, before, after Tree) error {
	if file {
		return s.transitionFile(ctx, before, after)
	}
	return s.transition(ctx, before, after)
}
