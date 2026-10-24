// SPDX-License-Identifier: Apache-2.0
// Recreates the .gitkeep that Vite's emptyOutDir removes from the desktop
// app's embed folder (desktop/main.go embeds it, so it must exist in git).
import { writeFileSync } from "node:fs";
import { join } from "node:path";

writeFileSync(join(process.argv[2], ".gitkeep"), "");
