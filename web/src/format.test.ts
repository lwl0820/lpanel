import { expect, test } from 'vitest'
import { errorText, formatDate, formatTime } from './format'
test('时间按本地时区显示到分钟', () => {
  const value = new Date(2026, 9, 29, 8, 5).toISOString()
  expect(formatTime(value)).toBe('2026-10-29 08:05')
  expect(formatDate(value)).toBe('2026-10-29')
})
test('空值和无效时间显示 —', () => {
  expect(formatTime('')).toBe('—')
  expect(formatDate('bad')).toBe('—')
})
test('错误信息取 message，没有时用默认文字', () => {
  expect(errorText(new Error('网络错误'), '失败')).toBe('网络错误')
  expect(errorText('x', '失败')).toBe('失败')
})
