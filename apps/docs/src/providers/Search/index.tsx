import React, { useEffect, useState } from "react"
import { SearchProvider as UiSearchProvider, checkArraySameElms } from "docs-ui"
import { ThemeConfig } from "@medusajs/docs"
import { useThemeConfig } from "@docusaurus/theme-common"
import { useLocalPathname } from "@docusaurus/theme-common/internal"

type SearchProviderProps = {
  children: React.ReactNode
}

const SearchProvider = ({ children }: SearchProviderProps) => {
  const [defaultFilters, setDefaultFilters] = useState<string[]>([])
  const { algoliaConfig: algolia } = useThemeConfig() as ThemeConfig
  const currentPath = useLocalPathname()

  useEffect(() => {
    let resultFilters = []
    algolia.defaultFiltersByPath.some((filtersByPath) => {
      if (currentPath.startsWith(filtersByPath.path)) {
        resultFilters = filtersByPath.filters
      }
    })
    if (!resultFilters.length && algolia.defaultFilters) {
      resultFilters = algolia.defaultFilters
    }
    if (!checkArraySameElms(defaultFilters, resultFilters)) {
      setDefaultFilters(resultFilters)
    }
  }, [currentPath])

  return (
    <UiSearchProvider
      algolia={{
        appId: algolia.appId,
        apiKey: algolia.apiKey,
        mainIndexName: algolia.indexNames.docs,
        indices: Object.values(algolia.indexNames),
      }}
      searchProps={{
        filterOptions: algolia.filters,
        suggestions: [
          {
            title: "Getting started? Try one of the following terms.",
            items: [
              "Install b binary manager",
              "Initialize project with b init",
              "Install binaries with b install",
              "Binary aliases",
              "Configuration with b.yaml",
            ],
          },
          {
            title: "Working with binaries",
            items: [
              "Version management",
              "Install with alias",
              "Update binaries",
              "Search available binaries",
              "Team collaboration",
            ],
          },
        ],
      }}
      commands={[]}
      initialDefaultFilters={defaultFilters}
      modalClassName="z-[500]"
    >
      {children}
    </UiSearchProvider>
  )
}

export default SearchProvider
