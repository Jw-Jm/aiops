import {defineConfig} from '@playwright/test';
export default defineConfig({testDir:'./tests/e2e',workers:1,retries:0,timeout:180000,fullyParallel:false,outputDir:process.env.OPS_WEB_QA_OUTPUT||'/data/ops-sp08-qa/test-results',reporter:[['line'],['json',{outputFile:process.env.OPS_WEB_QA_REPORT||'/data/ops-sp08-qa/playwright.json'}]],use:{trace:'off',screenshot:'off',video:'off'}});
