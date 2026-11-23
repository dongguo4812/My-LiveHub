//go:build lxproduction

package desktop

import "github.com/dongguo4812/My-LiveHub/app/internal/music/lxruntime"

// productionLXRuntime is the concrete runtime type linked into production
// desktop builds. Keeping the alias here makes the desktop composition root
// depend on the production build target rather than the PoC-only tag.
type productionLXRuntime = lxruntime.Runtime
