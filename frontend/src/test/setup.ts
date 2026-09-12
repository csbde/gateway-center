// T008 · Vitest 全局设置：jest-dom 匹配器 + localStorage 清洗。
import "@testing-library/jest-dom/vitest";
import { afterEach } from "vitest";

afterEach(() => localStorage.clear());
