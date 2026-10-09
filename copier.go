package copier

// Copy scaffolds a new project from a template.
//
// src is the template source (local path or Git URL).
// dst is the destination directory (created if it does not exist).
//
//	err := copier.Copy("gh:user/template", "./myproject",
//	    copier.WithData(map[string]any{"project_name": "myapp"}),
//	)
func Copy(src, dst string, opts ...Option) error {
	cfg := applyOptions(opts)
	cfg.SrcPath = src
	cfg.DstPath = dst

	w, err := newWorker(cfg, OpCopy)
	if err != nil {
		return err
	}
	return w.runCopy()
}

// Update updates an existing project to a newer version of its template using
// a 3-way merge. The destination must be a git repository containing the
// answers file from a previous Copy, and it must not have uncommitted changes.
//
//	err := copier.Update("./myproject",
//	    copier.WithConflict(copier.ConflictInline),
//	)
func Update(dst string, opts ...Option) error {
	cfg := applyOptions(opts)
	cfg.DstPath = dst

	w, err := newWorker(cfg, OpUpdate)
	if err != nil {
		return err
	}
	return w.runUpdate()
}

// Recopy re-applies a template to a project, discarding the project's
// evolution and using the existing answers.
//
//	err := copier.Recopy("./myproject")
func Recopy(dst string, opts ...Option) error {
	cfg := applyOptions(opts)
	cfg.DstPath = dst

	w, err := newWorker(cfg, OpCopy)
	if err != nil {
		return err
	}
	return w.runRecopy()
}
