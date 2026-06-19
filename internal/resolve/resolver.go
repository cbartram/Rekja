package resolve

import (
	"fmt"

	"github.com/cbartram/rekja/internal/manifest"
	"github.com/cbartram/rekja/internal/thunderstore"
)

// Plan is the resolved install/update graph.
type Plan struct {
	Roots        []ResolvedPackage
	Dependencies []ResolvedPackage
	Warnings     []string
}

// ResolvedPackage is a concrete package version selected for installation.
type ResolvedPackage struct {
	Package      thunderstore.Package
	Version      thunderstore.Version
	DependencyOf string
}

// Resolve selects versions for roots and transitive dependencies.
func Resolve(index map[string]thunderstore.Package, roots []manifest.TrackedMod) (Plan, error) {
	var plan Plan
	seen := map[string]ResolvedPackage{}

	for _, root := range roots {
		key := root.Key()
		pkg, ok := index[key]
		if !ok {
			return Plan{}, fmt.Errorf("tracked package not found in Thunderstore index: %s", key)
		}
		version, ok, err := thunderstore.FindVersion(pkg, root.DesiredVersion)
		if err != nil {
			return Plan{}, err
		}
		if !ok {
			return Plan{}, fmt.Errorf("version %q not found for %s", root.DesiredVersion, key)
		}
		resolved := ResolvedPackage{Package: pkg, Version: version}
		plan.Roots = append(plan.Roots, resolved)
		if err := resolveDependencies(index, resolved, seen, &plan); err != nil {
			return Plan{}, err
		}
	}
	return plan, nil
}

func resolveDependencies(index map[string]thunderstore.Package, parent ResolvedPackage, seen map[string]ResolvedPackage, plan *Plan) error {
	for _, dependency := range parent.Version.Dependencies {
		ref, err := thunderstore.ParseDependency(dependency)
		if err != nil {
			return err
		}
		key := ref.PackageKey()
		if key == "denikson-BepInExPack_Valheim" {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s declares %s; treating it as satisfied by ValheimPlus/BepInEx", parent.Version.FullName, dependency))
			continue
		}
		if existing, ok := seen[key]; ok {
			compare, err := thunderstore.CompareVersions(existing.Version.VersionNumber, ref.Version)
			if err != nil {
				return err
			}
			if compare < 0 {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s requires %s but %s already selected older %s", parent.Version.FullName, dependency, existing.DependencyOf, existing.Version.VersionNumber))
			}
			continue
		}

		pkg, ok := index[key]
		if !ok {
			return fmt.Errorf("dependency not found in Thunderstore index: %s", dependency)
		}
		version, ok, err := thunderstore.FindVersion(pkg, ref.Version)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("dependency version not found: %s", dependency)
		}
		resolved := ResolvedPackage{
			Package:      pkg,
			Version:      version,
			DependencyOf: parent.Version.FullName,
		}
		seen[key] = resolved
		plan.Dependencies = append(plan.Dependencies, resolved)
		if err := resolveDependencies(index, resolved, seen, plan); err != nil {
			return err
		}
	}
	return nil
}
