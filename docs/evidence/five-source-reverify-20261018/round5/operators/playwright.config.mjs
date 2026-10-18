import {defineConfig} from '@playwright/test';
export default defineConfig({testDir:'.',testMatch:'fault_chain_runtime.spec.ts',timeout:360000,workers:1,retries:0,reporter:[['list'],['json',{outputFile:process.env.OPS_WEB_QA_REPORT}]],outputDir:process.env.OPS_WEB_QA_OUTPUT,use:{trace:'off',video:'off',screenshot:'off'}});
