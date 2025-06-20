import styles from './styles.module.css'
import clsx from 'clsx'

interface ComponentProps extends React.ComponentProps<'button'> {
  primary?: boolean
  size?: 'small' | 'medium' | 'large'
  label: string
}

export function MyButton({ primary = false, size = 'medium', label, ...props }: ComponentProps) {
  const style = clsx(styles.button, {
    [styles['button--primary']]: primary,
    [styles[`button--${size}`]]: size,
  })

  return (
    <button
      type='button'
      className={style}
      {...props}
    >
      {label}
    </button>
  )
}
