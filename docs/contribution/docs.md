---
sidebar_label: "Docs"
sidebar_position: 1
---

# Docs Contribution Guidelines

Thank you for your interest in contributing to the documentation! You will be helping the open source community and other developers interested in learning more about b and using it.

:::tip

This guide is specific to contributing to the documentation. If you're interested in contributing to b's codebase, check out the [contributing guidelines in the b GitHub repository](https://github.com/fentas/b/blob/main/CONTRIBUTING.md).

:::

## Documentation Workspace

b's documentation is built with [Docusaurus](https://docusaurus.io/). The content lives on the `main` branch under the `docs/` directory, while the Docusaurus site configuration lives on the `docs` branch.

The workspace structure on the `docs` branch:

- `apps/docs`: the Docusaurus site that builds the documentation website.
- `packages`: shared UI components, Tailwind config, and TypeScript config.

During deployment, the CI copies `docs/` from `main` into `apps/docs/content` and builds the site.

---

## Documentation Content

### Writing Documentation

The documentation content is written in Markdown and MDX format and is located in the [`docs/`](https://github.com/fentas/b/tree/main/docs) directory of the **b** repository on the `main` branch.

If you're not familiar with Markdown, check out [this cheat sheet](https://www.markdownguide.org/cheat-sheet/) for a quick start. MDX files can contain JSX components and import statements — you can learn more about [MDX in Docusaurus's guide](https://docusaurus.io/docs/markdown-features/react).

## Style Guide

When you contribute to the documentation content, make sure to follow the [documentation style guide](https://www.notion.so/Style-Guide-Docs-fad86dd1c5f84b48b145e959f36628e0).

---

## How to Contribute

If you're fixing errors in an existing documentation page, you can scroll down to the end of the page and click on the "Edit this page" link. You'll be redirected to the GitHub edit form of that page and you can make edits directly and submit a pull request (PR).

If you're adding a new page or contributing to the codebase, fork the repository, create a new branch, and make all changes necessary. Then create a PR in the **b** repository.

### Base Branch

Documentation contributions always use `main` as the base branch. Make sure to also open your PR against the `main` branch.

### Pull Request Conventions

When you create a pull request, prefix the title with `docs:`.

<!-- vale off -->

In the body of the PR, explain clearly what the PR does. If the PR solves an issue, use [closing keywords](https://docs.github.com/en/issues/tracking-your-work-with-issues/linking-a-pull-request-to-an-issue#linking-a-pull-request-to-an-issue-using-a-keyword) with the issue number. For example, "Closes #1333".

<!-- vale on -->

---

## Sidebar Configuration

When you add a new page to the documentation, you must add the new page in `apps/docs/sidebars.js` on the `docs` branch. The properties of the exported object indicate sidebar names, and the values are arrays of sidebar items.

You can learn more about the [sidebar item syntax in the Docusaurus docs](https://docusaurus.io/docs/sidebar/items).

### Terminology

When the documentation page is a conceptual or an overview documentation, the label in the sidebar should start with a noun.

When the documentation page is tutorial documentation, the label in the sidebar should start with a verb. Exceptions to this rule are integration documentation and upgrade guides.

### Sidebar Icons

To add an icon to a sidebar item, check if the icon is already exported in `apps/docs/src/theme/Icon`. If not, add the new icon as a React component in `apps/docs/src/theme/Icon/Icon<Name>/index.tsx`, where `<Name>` is the camel-case name of your icon. The icon must use an SVG element with `stroke` or `fill` set to `currentColor`.

For example:

```tsx title="apps/docs/src/theme/Icon/Bolt/index.tsx"
import React from "react"

interface IconProps {
  width?: number
  height?: number
}

export default function IconBolt(props: IconProps) {
  return (
    <svg
      width={props.width || 20}
      height={props.height || 20}
      viewBox="0 0 20 20"
      fill="none" xmlns="http://www.w3.org/2000/svg"
      {...props}
    >
      <path
        d="M3.125..."
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        stroke="currentColor" />
    </svg>
  )
}
```

Then register it in `apps/docs/src/theme/Icon/index.ts`:

```ts title="apps/docs/src/theme/Icon/index.ts"
import IconBolt from "./Bolt"
import IconBoltSolid from "./BoltSolid"
// other imports

export default {
  // other icons
  "bolt": IconBolt,
  "bolt-solid": IconBoltSolid,
  // other icons
}
```

Finally, reference the icon via the `sidebar_icon` custom prop:

```js title="apps/docs/sidebars.js"
module.exports = {
  // other sidebars
  homepage: [
    {
      // other properties
      customProps: {
        sidebar_icon: "book-open",
      },
    },
    // other items
  ],
}
```

### Sidebar Item Types

There are different sidebar item types used in the documentation:

- Homepage Items: If a sidebar item is shown under the `homepage` sidebar, set the `className` property to `homepage-sidebar-item`.

- Sidebar Title: Add `sidebar_is_title: true` to the `customProps` object. Typically combined with an icon.

- Back Item: Add `sidebar_is_back_link: true` and `sidebar_icon: "back-arrow"` to `customProps`.

- Group Divider: Use `type: "html"` with `sidebar_is_group_divider: true` in `customProps`.

- Group Headline: Use `type: "category"` with `sidebar_is_group_headline: true` in `customProps`.

- Soon Item: Use `type: "link"`, `href: "#"`, and `sidebar_is_soon: true` in `customProps` to indicate upcoming content.

---

## Notes and Additional Information

When displaying notes and additional information, use [Admonitions](https://docusaurus.io/docs/markdown-features/admonitions). Match the type to the note's importance:

- `danger` for critical warnings
- `tip` for helpful information outside the page's scope
- `note` for all other notes

---

## Images

If you are adding images to a documentation page, you can host the image on [Imgur](https://imgur.com) for free to include it in the PR. Our team will later upload it to our image hosting.

---

## Code Blocks

### Use Tabs with Code Blocks

To use Tabs with Code Blocks, use [Docusaurus's `Tabs` and `TabItem` components](https://docusaurus.io/docs/markdown-features/code-blocks#multi-language-support-code-blocks). Pass the `isCodeTabs={true}` prop for correct styling.

For example:

~~~md
import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

<Tabs groupId="request-type" isCodeTabs={true}>
  <TabItem value="install" label="Install Binary" default>

    ```bash
    b install jq
    ```

  </TabItem>
  <TabItem value="add" label="Add to Config">

    ```bash
    b install --add jq
    ```

  </TabItem>
</Tabs>
~~~

### Add Title to Code Block with Tabs

Add the `codeTitle` prop to the `Tabs` component:

```md
<Tabs
  groupId="request-type"
  isCodeTabs={true}
  codeTitle="/src/services/hello.ts">
```

### Add Title to Code Block without Tabs

~~~md
```js title="src/index.ts"
console.log("hello")
```
~~~

### Remove Report Button

Use the `noReport` metadata to remove the report button:

~~~md
```bash noReport
b install jq
```
~~~

### Remove Copy Button

Use the `noCopy` metadata to remove the copy button:

~~~md
```bash noCopy
source ~/.bashrc
```
~~~

---

## Linting with Vale

b uses [Vale](https://vale.sh/) to lint documentation pages and perform checks on incoming PRs.

### Result of Vale PR Checks

You can check the result of running the "lint" action on your PR by clicking the Details link next to it. You can find there all errors that you need to fix.

### Run Vale Locally

1. [Install direnv](https://direnv.net/) and run `direnv allow` in the root directory.
2. Lint with `vale`:

```bash
vale docs/
```

### VS Code Extension

You can optionally use the [Vale VS Code extension](https://marketplace.visualstudio.com/items?itemName=chrischinchilla.vale-vscode) for inline error highlighting while writing.

### Linter Exceptions

If you need to break style guide rules in a document:

```md
<!-- vale off -->

content that shouldn't be scanned for errors here...

<!-- vale on -->
```

You can also disable specific rules:

```md
<!-- vale docs.Numbers = NO -->

b supports Bash version 4.3 and later.

<!-- vale docs.Numbers = YES -->
```

If you use this in your PR, you must justify its usage.

---

## Linting with ESLint

b uses ESLint to lint code blocks in both content and the documentation site codebase.

### Linting Code/Content with ESLint

Each PR runs through a check that lints code in the content files using ESLint. The action's name is `code-docs-eslint`.

To check and fix ESLint errors locally, run from the `docs` branch root:

```bash
yarn lint:fix
```

### ESLint Exceptions

To skip linting for a specific code block:

~~~md
<!-- eslint-skip -->

```js
console.log("This block isn't linted")
```

```js
console.log("This block is linted")
```
~~~

To disable specific rules:

~~~md
<!-- eslint-disable semi -->

```js
console.log("This block can use semicolons");
```

```js
console.log("This block can't use semi colons")
```
~~~

---
