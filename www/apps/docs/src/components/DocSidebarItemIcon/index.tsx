import React from "react"
import icons, { IconName } from "../../theme/Icon"
import BorderedIcon from "../BorderedIcon"
import clsx from "clsx"

type DocSidebarItemIconProps = {
  icon?: IconName
  is_title?: boolean
  is_disabled?: boolean
} & React.HTMLAttributes<HTMLSpanElement>

const DocSidebarItemIcon: React.FC<DocSidebarItemIconProps> = ({
  icon,
  is_title,
  is_disabled,
}) => {
  let IconComponent = undefined
  if (icon) {
    IconComponent = icons[icon]
  }

  return (
    <>
      {is_title && (
        <BorderedIcon
          icon={undefined}
          IconComponent={IconComponent}
          iconClassName={clsx("sidebar-item-icon")}
          iconColorClassName={clsx(
            "text-medusa-fg-subtle",
            is_disabled && "text-medusa-fg-disabled"
          )}
        />
      )}
      {!is_title && IconComponent && (
        <IconComponent
          className={clsx(
            "sidebar-item-icon",
            "text-medusa-fg-subtle",
            is_disabled && "text-medusa-fg-disabled"
          )}
        />
      )}
    </>
  )
}

export default DocSidebarItemIcon
