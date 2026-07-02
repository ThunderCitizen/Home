UPDATE public.budget_ledger
SET amount = 23043400.00,
    budget_type = 'capital',
    source_hash = 'e98754e27268b8f3abb5011606d361cb9bfac7c0acd598a86f78bb84d2476b9b'
WHERE fiscal_year = 2026
  AND debit_code = 'service.capital_budget.capital_budget'
  AND credit_code = 'revenue.property_tax'
  AND source_hash = 'b9e6cf666cfe74527721398fbd7306038a1f02c5852bca22df906694999f713d';
